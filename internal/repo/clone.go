// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrTargetNotEmpty reports a clone target that already holds something.
//
// It is a runtime failure rather than a usage error: the command was well
// formed, and the answer is to move or remove what is there, or to name another
// directory. What it is NOT is a thing canga resolves on its own — nothing here
// ever merges into or overwrites an existing tree.
var ErrTargetNotEmpty = errors.New("refusing to clone into a non-empty path")

// envBaseDir overrides the root of the deterministic clone layout.
const envBaseDir = "CANGA_HOST_BASE_DIR"

// clonePerm is what the PARENT directories of a clone are created with. git
// creates the clone itself, under the caller's umask, and this does not touch it.
const clonePerm fs.FileMode = 0o755

// CloneOptions carries everything about a clone that is not the URL.
type CloneOptions struct {
	// Dir is an explicit target. Empty means derive it from the URL, which is
	// the whole point of the command; an explicit one exists for the cases the
	// layout cannot express.
	Dir string

	// NoProgress drops git's `\r` progress meter. Set it when the output is not
	// a terminal a human is watching.
	NoProgress bool

	// Out and Err are where git's own output goes. A nil writer discards it.
	Out io.Writer
	Err io.Writer
}

// CloneResult is what a finished clone left behind.
type CloneResult struct {
	// Dir is the resolved absolute path the repository was cloned into.
	Dir string

	// Signing is what was stamped into the clone's local config.
	Signing Signing

	// StampErr is a failure to write that signing configuration. It is a FIELD
	// rather than a returned error because it is not one: the clone exists, it
	// is complete, and it is what the caller asked for. Only its signing setup
	// is missing, which the caller reports as a warning and the user fixes with
	// one `git config`.
	StampErr error
}

// Clone clones a repository into the deterministic layout, or into an explicit
// directory, and stamps signing configuration into it.
//
// The transport hardening in transportFlags applies, so a URL naming the ext or
// fd remote helper is refused by git itself rather than executed.
func Clone(ctx context.Context, url string, opts CloneOptions) (CloneResult, error) {
	target, err := resolveTarget(url, opts.Dir)
	if err != nil {
		return CloneResult{}, err
	}

	if err := requireEmpty(target); err != nil {
		return CloneResult{}, err
	}

	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, clonePerm); err != nil {
		return CloneResult{}, fmt.Errorf("creating %s: %w", parent, err)
	}

	args := []string{"clone"}
	if opts.NoProgress {
		args = append(args, "--no-progress")
	}

	// The "--" is load-bearing: without it a URL beginning with a dash is read
	// as an option, and git reports something that has nothing to do with the
	// repository the user named.
	args = append(args, "--", url, target)

	// No -C: the target does not exist yet, and there is no repository to run in.
	if err := streamGit(ctx, "", transportFlags(), opts.Out, opts.Err, args...); err != nil {
		return CloneResult{}, err
	}

	result := CloneResult{Dir: target}
	result.Signing, result.StampErr = StampSigning(ctx, target)

	return result, nil
}

// TargetDir is where Clone puts a repository when no directory is named:
// ${CANGA_HOST_BASE_DIR:-$HOME/src} joined with the URL's "<host>/<owner>/<repo>".
//
// The tail keeps its readable spelling here, and is deliberately NOT passed
// through EscapePath the way the reminder store's directory is. A clone is a
// path a human types and reads every day, and unlike the store it is never
// shared across two filesystems that disagree about case.
func TargetDir(url string) (string, error) {
	tail, err := Path(url)
	if err != nil {
		return "", err
	}

	base, err := BaseDir()
	if err != nil {
		return "", err
	}

	// Path has already refused a "." or ".." segment, which is what keeps this
	// join inside base.
	return filepath.Join(base, filepath.FromSlash(tail)), nil
}

// BaseDir is the root of the deterministic clone layout, as an absolute path.
//
// Absolute even when CANGA_HOST_BASE_DIR is not, because the derived path is
// PRINTED for a caller to use: `cd $(canga clone <url>)` from another directory
// needs an answer that does not depend on where canga was standing. It is also
// what makes the non-empty check and the clone itself agree about one place.
func BaseDir() (string, error) {
	dir := os.Getenv(envBaseDir)
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving the clone base directory: %w", err)
		}

		dir = filepath.Join(home, "src")
	}

	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving the clone base directory %s: %w", dir, err)
	}

	return absolute, nil
}

// resolveTarget decides where a clone lands.
func resolveTarget(url, dir string) (string, error) {
	if dir == "" {
		return TargetDir(url)
	}

	// Absolute, because the resolved path is printed on stdout for a caller to
	// use, and a relative one means nothing to a caller in another directory.
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", dir, err)
	}

	return absolute, nil
}

// requireEmpty refuses a target that already holds something.
//
// An EXISTING EMPTY directory is accepted, which is not an oversight: `mkdir
// ~/src/thing && canga clone … ~/src/thing` is a normal thing to do, and git
// accepts it too.
func requireEmpty(target string) error {
	entries, err := os.ReadDir(target)

	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		// Anything else — a file in the way, a directory that cannot be read —
		// is still a target that cannot be cloned into. git's own error would be
		// clearer, but it would arrive after a full clone had been fetched.
		return fmt.Errorf("%w: %s: %w", ErrTargetNotEmpty, target, err)
	case len(entries) > 0:
		return fmt.Errorf("%w: %s", ErrTargetNotEmpty, target)
	}

	return nil
}
