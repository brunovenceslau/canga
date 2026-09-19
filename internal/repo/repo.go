// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package repo owns the git working trees canga operates on: their
// deterministic identity — the URL of the origin remote and the
// "<host>/<owner>/<repo>" path tail every per-repo artifact is keyed by — and
// the two operations that bring a tree into existence and advance it, Clone and
// Sync.
//
// The tail is what makes the layout deterministic: the same three segments
// address the clone on disk, the reminder store, and `canga workspace`,
// whatever protocol the repository was cloned with.
//
// dotfiles-host still carries the original zsh implementation (zsh/dev.zsh),
// which is retired there once a canga release is installed on both machines.
// Until then the derivation exists twice, and the two are cross-checked against
// the same cases as its tests/dev_test.sh.
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

	// ErrNotARepository reports a directory that is not inside a git working
	// tree. Every canga subcommand is keyed by a repository, so this is the
	// caller standing in the wrong place rather than anything having failed.
	ErrNotARepository = errors.New("not inside a git repository")
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
func Origin(ctx context.Context, dir string) (string, error) {
	url, err := git(ctx, dir, ErrNoOrigin, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}

	if url == "" {
		return "", fmt.Errorf("%w in %s", ErrNoOrigin, dir)
	}

	return url, nil
}

// Root returns the top level of the working tree dir belongs to. dir may be any
// subdirectory of it.
func Root(ctx context.Context, dir string) (string, error) {
	return git(ctx, dir, ErrNotARepository, "rev-parse", "--show-toplevel")
}

// CommonDir returns the git directory hooks are run from, as an absolute path.
// It is the COMMON one, so a linked worktree resolves to the hooks its main
// working tree uses rather than to a directory git never reads.
func CommonDir(ctx context.Context, dir string) (string, error) {
	return git(ctx, dir, ErrNotARepository, "rev-parse", "--path-format=absolute", "--git-common-dir")
}

// Config reads one git configuration value, and reports whether it is SET.
//
// The bool is not decoration. git answers an unset key by exiting 1 with no
// output and a key set to the empty string by exiting 0 with no output, and
// those mean opposite things for core.hooksPath: unset leaves git reading
// $GIT_DIR/hooks, while set-to-empty makes it read nothing at all. Returning
// only the value would collapse the two.
//
// It does not go through the shared helper, because it needs the exit code the
// helper deliberately hides.
//
// Two things about what it returns. It is the EFFECTIVE value, so a key set
// globally is reported even though the repository does not set it, which is
// right: the effective value is the one git will obey. And `git config --get`
// does not need a repository at all, so outside one this reads the global
// configuration rather than failing. Callers that mean the repository's own
// setting resolve Root first, which is where the real refusal happens.
//
// SetConfig, by contrast, writes LOCALLY. Reading what git will obey and
// writing only where canga was invited are deliberately different scopes.
func Config(ctx context.Context, dir, key string) (value string, set bool, err error) {
	out, err := gitCommand(ctx, dir, nil, []string{"config", "--get", key}).Output()
	if err == nil {
		return strings.TrimSpace(string(out)), true, nil
	}

	if exit, ran := errors.AsType[*exec.ExitError](err); ran && exit.ExitCode() == 1 {
		return "", false, nil
	}

	return "", false, classify(ctx, dir, ErrNotARepository, err)
}

// GlobalConfig reads one git configuration value from the user's GLOBAL config
// alone, and answers an unset key with the empty string rather than an error.
//
// The scope is the point, which is why this exists beside Config rather than as
// a flag on it. Config reads the EFFECTIVE value, so inside a repository it
// reports that repository's own setting; this is asked for the identity of the
// MACHINE — the signing key a new clone should inherit — and a repo-local key
// belonging to whatever repository canga happened to be invoked in is exactly
// the wrong answer. Ported from zsh/dev.zsh, where the `--global` on those two
// reads carries the same comment.
func GlobalConfig(ctx context.Context, key string) (string, error) {
	out, err := gitCommand(ctx, "", nil, []string{"config", "--global", "--get", key}).Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}

	// The context is checked FIRST, before the exit status, because cancellation
	// arrives AS an exit status: CommandContext kills git, and the ExitError that
	// leaves behind reports -1. Reading that as git's own answer would report
	// "git exited -1" for a Ctrl-C.
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("reading %s from the global git config: %w", key, ctxErr)
	}

	if exit, ran := errors.AsType[*exec.ExitError](err); ran {
		// Exit 1 is git's answer for "no such key", which is not a failure here:
		// most machines set neither of the two keys this reads, and the caller's
		// contract is to leave a clone alone when they do not.
		if exit.ExitCode() == 1 {
			return "", nil
		}

		return "", fmt.Errorf("reading %s from the global git config: git exited %d%s",
			key, exit.ExitCode(), gitSaid(exit))
	}

	// Only a missing binary can reach classify here: cancellation and every exit
	// status were answered above, which is why the sentinel it is handed never
	// appears in the message.
	return "", classify(ctx, ".", ErrNotARepository, err)
}

// SetConfig writes one git configuration value into the repository's own
// config, never a global or system one.
func SetConfig(ctx context.Context, dir, key, value string) error {
	_, err := git(ctx, dir, ErrNotARepository, "config", key, value)

	return err
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

	redacted := Redact(rawURL)

	for _, segment := range segments {
		if !isSafeSegment(segment) {
			// The offending segment is a substring of the RAW url, so quoting it
			// is only safe when nothing was redacted out of that url. A
			// credential holding an unencoded "/" is split by `split` and its
			// tail lands inside a segment: quoting that segment hands over the
			// second half of the secret while the url beside it is redacted,
			// which is worse than saying less. Measured on exactly the input the
			// redaction exists for.
			if redacted == rawURL {
				return "", fmt.Errorf("%w: refusing path segment %q in %s", ErrBadURL, segment, redacted)
			}

			return "", fmt.Errorf("%w: refusing an unusable path segment in %s", ErrBadURL, redacted)
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
	if _, after, ok := strings.CutLast(authority, "@"); ok {
		authority = after
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
//
// It cuts at the LAST "@" in everything after the scheme, rather than isolating
// the authority first and cutting inside that. Isolating first looks tidier and
// is wrong: a credential containing an unencoded "/" puts the split before the
// "@", the "@" is then never found, and the secret is echoed verbatim. Measured,
// on the one input where it matters most, because such a URL also fails to parse
// and so is the very thing this gets asked to print.
//
// The cost is over-redaction when a PATH contains an "@", and it is not a lost
// hostname but the whole url: "https://github.com/o/r@v2" redacts to
// "https://v2". The trade is still the right one, because a diagnostic that
// says too little is an inconvenience and a leaked token is not recoverable.
func Redact(url string) string {
	scheme := ""
	if before, after, ok := strings.Cut(url, "://"); ok {
		scheme, url = before+"://", after
	}

	if _, after, ok := strings.CutLast(url, "@"); ok {
		url = after
	}

	return scheme + url
}
