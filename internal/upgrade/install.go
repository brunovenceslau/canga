// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// defaultBinaryPerm is what a replacement is left as when there is nothing to
// inherit from. The archive's own mode is deliberately not honoured: what
// canga writes is canga's decision, not the archive's.
const defaultBinaryPerm fs.FileMode = 0o755

// ownerExec is the one bit an install cannot do without. Whatever else is
// preserved, the person who just ran the upgrade has to be able to run what it
// produced.
const ownerExec fs.FileMode = 0o100

// groupOtherWrite is the half of the mode that is NOT inherited. Preserving a
// deliberate 0o700 is the point; preserving a 0o777 that came from a zero
// umask, or from reading the file off a FAT volume, is preserving a mistake —
// and it would put a world-writable executable in the install directory during
// the window between staging and rename, before anything has verified it.
const groupOtherWrite fs.FileMode = 0o022

// execTimeout bounds the sanity check run against the staged binary. It prints
// a version string and exits; anything slower is broken.
const execTimeout = 30 * time.Second

// How many times the sanity check retries ETXTBSY, and how long it waits first.
//
// "text file busy" is what the kernel answers when a file is executed while
// some process still holds it open for writing. canga closes the staged file
// before running it, so the writer is never canga itself: it is a fork that
// happened to inherit another thread's write descriptor in the window before
// its exec. That is a race against the runtime, not a property of the file,
// and it was MEASURED here — the parallel install tests reproduced it — rather
// than guarded against on principle. The delay doubles each time, so the whole
// retry costs at most a few milliseconds.
const (
	execAttempts   = 5
	execRetryDelay = 2 * time.Millisecond
)

// ErrWrongBinary reports a staged file that does not run, or does not report
// the version it was downloaded as. It is the check that turns a wrong-platform
// download into a refusal instead of an unusable canga on PATH.
var ErrWrongBinary = errors.New("the downloaded binary does not report the expected version and build")

// target resolves the file this process is running from.
//
// Symlinks are followed on purpose. ~/.local/bin is where the dotfiles link
// engine puts symlinks to tracked files, so replacing the NAME rather than the
// file behind it would silently convert one of those links into a regular file
// and break that layer's contract.
//
// invoked is the unresolved path, and it is reported only when it differs —
// which depends on the operating system, not on how canga was invoked. On
// linux os.Executable reads /proc/self/exe, which the kernel has ALREADY
// resolved, so the two are always equal there and nothing is reported even when
// a symlink was used. On darwin the kernel hands back the path as given, so a
// /var to /private/var resolution does show up. The file replaced is the right
// one either way; only the diagnostic varies.
func target() (path, invoked string, err error) {
	invoked, err = os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("locate the running canga: %w", err)
	}

	path, err = filepath.EvalSymlinks(invoked)
	if err != nil {
		return "", "", fmt.Errorf("resolve %s: %w", invoked, err)
	}

	if path == invoked {
		invoked = ""
	}

	return path, invoked, nil
}

// replace swaps binary in for the file at path, atomically.
//
// The staging file is created in path's OWN directory, which is the whole
// trick: os.Rename is atomic only within a filesystem, so the usual
// os.CreateTemp("") would work on a laptop and fail with EXDEV wherever /tmp is
// a different mount. Writing there is also the only place this is allowed to
// write — it is the install directory, by definition.
//
// The order is write, flush to disk, run the result, and only then rename.
// Renaming first would put an unverified file on PATH; flushing after would let
// a crash leave a directory entry pointing at a half-written executable. On
// unix the running process keeps its own inode alive, so replacing the file
// under it is safe.
func replace(ctx context.Context, path string, binary []byte, wantTag, wantRole string) (err error) {
	staged, err := os.CreateTemp(filepath.Dir(path), ".canga-upgrade-*")
	if err != nil {
		return fmt.Errorf("stage a new binary beside %s: %w", path, err)
	}

	name := staged.Name()

	// Closed twice on the happy path — the second one answers os.ErrClosed and
	// is ignored. The alternative is leaving a descriptor open on every early
	// return, which is worse for being invisible.
	defer func() { _ = staged.Close() }()

	// A run that fails must leave NOTHING behind: a stale .canga-upgrade-*
	// beside the binary is confusing at best and executable at worst.
	defer func() {
		if err != nil {
			_ = os.Remove(name)
		}
	}()

	if err = writeStaged(staged, binary, installPerm(path)); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}

	if err = verifyRuns(ctx, name, wantTag, wantRole); err != nil {
		return err
	}

	if err = os.Rename(name, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}

	return nil
}

// installPerm is the mode a replacement should be left as: the one the file it
// replaces already carries.
//
// Inheriting rather than imposing 0o755 is the difference between an upgrade
// and a quiet policy change. A canga deliberately installed 0o700 on a shared
// host would otherwise become world-executable on the first upgrade, with
// nothing said about it — a decision the user made, undone by a command that
// was only asked to change the version.
//
// What is inherited is only the half worth inheriting; see groupOtherWrite.
func installPerm(path string) fs.FileMode {
	info, err := os.Stat(path)
	if err != nil {
		return defaultBinaryPerm
	}

	return info.Mode().Perm()&^groupOtherWrite | ownerExec
}

// writeStaged fills the staging file and gets it onto the disk.
func writeStaged(staged *os.File, binary []byte, perm fs.FileMode) error {
	if _, err := staged.Write(binary); err != nil {
		return err
	}

	// Chmod on the open file rather than the path: it cannot race with anything
	// that replaces the name in between.
	if err := staged.Chmod(perm); err != nil {
		return err
	}

	// Sync before the rename, not after: rename publishes the directory entry
	// atomically, but says nothing about the data behind it having reached the
	// disk.
	if err := staged.Sync(); err != nil {
		return err
	}

	return staged.Close()
}

// verifyRuns executes the staged binary and requires it to report the version
// that was downloaded, from the build that is running.
//
// It runs only AFTER the checksum has been verified — never before — so this is
// not a new trust decision, it is a last check that the file is what it claims:
// a binary for the wrong platform, or an archive entry that is not canga at
// all, fails here instead of after it has taken the place of a working install.
// The version it prints comes from the new binary itself, so what the command
// reports afterwards is observed rather than assumed.
//
// The role is checked for the same reason the archive is picked by it: a
// sandbox build must never be replaced by the host build, whatever a release's
// archive happens to be called.
func verifyRuns(ctx context.Context, path, wantTag, wantRole string) error {
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()

	out, err := runVersion(ctx, path)
	if err != nil {
		return fmt.Errorf("%w: %s did not run: %w", ErrWrongBinary, filepath.Base(path), err)
	}

	reported, role := parseVersion(string(out))
	if !sameTag(reported, wantTag) || role != wantRole {
		return fmt.Errorf("%w: it reports %q (%s), not %s (%s)", ErrWrongBinary, reported, role, wantTag, wantRole)
	}

	return nil
}

// parseVersion reads the two fields verifyRuns checks out of `canga --version`,
// which prints "<version> (<role>, <commit>, <goversion>)". Output of another
// shape yields a role that names no build, so the comparison fails.
func parseVersion(out string) (version, role string) {
	version, rest, _ := strings.Cut(strings.TrimSpace(out), " ")

	if details, ok := strings.CutPrefix(rest, "("); ok {
		role, _, _ = strings.Cut(details, ",")
	}

	return version, role
}

// runVersion executes path with --version, retrying only ETXTBSY.
//
// Every other failure is returned on the first attempt: a binary for the wrong
// platform does not become the right one by being asked twice.
func runVersion(ctx context.Context, path string) ([]byte, error) {
	var err error

	for attempt := range execAttempts {
		var out []byte

		// install directory, and the argument is a literal; nothing here comes
		// from the command line or from the archive.
		out, err = exec.CommandContext(ctx, path, "--version").Output()
		if err == nil {
			return out, nil
		}

		if !errors.Is(err, syscall.ETXTBSY) {
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(execRetryDelay << attempt):
		}
	}

	return nil, err
}
