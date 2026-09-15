// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package upgrade replaces the running devctl with a binary from a GitHub
// release.
//
// It is NOT the same mechanism as dotfiles-upgrade, which is git-fetch based
// and updates a checkout. This downloads a release artifact and swaps one
// executable file for another, so the two share a verb and nothing else.
//
// Three properties are the whole point of the package.
//
// The release source is fixed at compile time. Nothing in the environment can
// point devctl at another host, because whoever set that variable would be
// choosing the code this binary replaces itself with.
//
// Every artifact is checked against the SHA-256 the release publishes in
// checksums.txt, and an artifact with no checksum line of its own is refused.
// That proves integrity, not authenticity: the same account that publishes the
// asset publishes the checksum, so this catches a corrupted or truncated
// download and nothing more. The README says so in those words.
//
// The replacement is atomic. The new binary is written beside the old one, in
// the install directory and nowhere else, flushed, executed once to confirm it
// is what it claims to be, and only then renamed over the target — so an
// interrupted upgrade cannot leave a half-written executable on PATH.
package upgrade

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrNotRelease reports a binary that was not built from a published
	// release — `go install`, or a build from a tree that has moved past its
	// last tag. There is no version to compare against, so the run is refused
	// rather than guessed at; --tag is the way to say which release is meant.
	ErrNotRelease = errors.New("this devctl was not built from a release")

	// ErrBadTag reports a --tag that is not a version.
	ErrBadTag = errors.New("not a release tag")
)

// Options is one upgrade run.
type Options struct {
	// Current is the version stamped into the running binary. It is passed in
	// rather than read from a package variable so that a run is fully
	// determined by its arguments.
	Current string

	// Token authenticates against the GitHub API. devctl's repository is
	// private, so there is no unauthenticated path; see Token.
	Token string

	// Tag installs that exact release instead of the newest one. It is the only
	// way to re-install, to step deliberately backwards, or to upgrade from a
	// binary that reports no release version.
	Tag string

	// Check resolves and reports what is available without changing anything.
	Check bool

	// baseURL and path are test seams, not settings. An environment override
	// for either would let whoever set it choose both the code devctl installs
	// and where it lands.
	baseURL string
	path    string
}

// Result is what a run did, or would have done. It carries enough for the
// caller to report the outcome without repeating any of the decisions.
type Result struct {
	Current   string // the version that was running, normalized
	Release   string // the release this run resolved
	Path      string // the file that was, or would be, replaced
	Invoked   string // the symlink that led to Path, when one did
	Newer     bool   // the resolved release is a later version than Current
	Installed bool   // false for --check, and when nothing was newer
}

// Run resolves a release and, unless Options.Check is set, installs it.
//
// It is deliberately ordered so that everything that can be refused cheaply is
// refused before anything is downloaded, and nothing is written until the
// download has been verified.
func Run(ctx context.Context, opts Options) (Result, error) {
	wanted, err := wantedTag(opts)
	if err != nil {
		return Result{}, err
	}

	if opts.Token == "" {
		return Result{}, fmt.Errorf("%w", ErrNoToken)
	}

	result := Result{Current: normalizeTag(opts.Current)}

	result.Path, result.Invoked, err = resolveTarget(opts)
	if err != nil {
		return Result{}, err
	}

	found, err := resolveRelease(ctx, opts, wanted)
	if err != nil {
		return Result{}, err
	}

	result.Release = found.Tag
	result.Newer = isNewer(result.Current, found.Tag)

	// An explicit --tag is an instruction, not a comparison: it re-installs the
	// version it names even when that is the one already running.
	if opts.Check || (opts.Tag == "" && !result.Newer) {
		return result, nil
	}

	binary, err := fetchBinary(ctx, opts, found)
	if err != nil {
		return result, err
	}

	if err := replace(ctx, result.Path, binary, found.Tag); err != nil {
		return result, err
	}

	result.Installed = true

	return result, nil
}

// wantedTag decides which release this run is about, and refuses the runs that
// cannot be decided at all.
func wantedTag(opts Options) (string, error) {
	if opts.Tag != "" {
		tag := normalizeTag(opts.Tag)
		if !isReleaseTag(tag) {
			return "", fmt.Errorf("%w: %q", ErrBadTag, opts.Tag)
		}

		return tag, nil
	}

	if _, isRelease := releaseTag(opts.Current); !isRelease {
		return "", fmt.Errorf(
			"%w: it reports %q, so there is nothing to compare a release against; pass --tag to name one",
			ErrNotRelease, opts.Current)
	}

	return "", nil
}

// resolveTarget answers which file would be replaced. It runs before the
// network does, so a devctl that cannot locate itself says that instead of
// downloading six megabytes first.
func resolveTarget(opts Options) (path, invoked string, err error) {
	if opts.path != "" {
		return opts.path, "", nil
	}

	return target()
}

func resolveRelease(ctx context.Context, opts Options, wanted string) (release, error) {
	api := newClient(opts.Token, opts.baseURL, opts.Current)

	if wanted != "" {
		return api.byTag(ctx, wanted)
	}

	return api.latest(ctx)
}

// fetchBinary downloads a release's archive, proves it is the one the release
// names, and returns the devctl inside it.
func fetchBinary(ctx context.Context, opts Options, found release) ([]byte, error) {
	archiveAsset, checksumsAsset, err := pickAssets(found)
	if err != nil {
		return nil, err
	}

	api := newClient(opts.Token, opts.baseURL, opts.Current)

	checksums, err := api.download(ctx, checksumsAsset, maxChecksumsBytes)
	if err != nil {
		return nil, err
	}

	archive, err := api.download(ctx, archiveAsset, maxArchiveBytes)
	if err != nil {
		return nil, err
	}

	if err := verifyChecksum(archive, checksums, archiveAsset.Name); err != nil {
		return nil, err
	}

	return extractBinary(archive)
}
