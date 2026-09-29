// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/brunovenceslau/canga/internal/boundedread"
)

// A reminders key is a LABEL, not an authenticated identity. It says which
// list a repository reads, and nothing about who may read it: an origin is
// already whatever the repository's own configuration says, and anything able
// to write a repository's .git/config can point it at any key. What the
// location-derived key adds is a guarantee about WHICH directory it names,
// the one the process was pointed at, found on the filesystem, never a
// directory the repository's configuration chose.

var (
	// ErrNotUnderBase reports a working tree that is not strictly below the
	// base directory, so no key can be taken from its location. The base
	// itself is refused too: the key would be empty, and an empty key puts the
	// store at the root every other key hangs under.
	ErrNotUnderBase = errors.New("not below the base directory")

	// ErrNoSuchBase reports a base directory that does not exist, so nothing
	// can be below it. Its own sentinel rather than ErrNotUnderBase, because
	// the fix is different: create the directory or correct the variable.
	ErrNoSuchBase = errors.New("the base directory does not exist")

	// ErrBadPath reports a location below the base whose segments a store
	// directory cannot hold: the rule a URL's segments are held to, plus the
	// names the store itself uses inside a key's directory.
	ErrBadPath = errors.New("unusable repository path")

	// ErrWorktreeElsewhere reports a repository whose git configuration, or
	// environment, places its working tree somewhere other than the directory
	// holding its .git. Keying such a tree by either answer would let
	// configuration inside the repository choose the key.
	ErrWorktreeElsewhere = errors.New("git places the working tree away from its .git")

	// ErrWorktreeUnverified reports a linked worktree whose link to its main
	// working tree does not check out in both directions. A linked worktree
	// is keyed by its main tree, so the link decides the key, and a link that
	// only one side of the repository asserts is not believed.
	ErrWorktreeUnverified = errors.New("linked worktree does not match its main repository")

	// ErrDotGitLink reports a working tree whose .git is a symbolic link. A
	// linked .git lets one directory pass for another repository's main tree
	// or worktree, whose key it would then read and write, so it is refused
	// before anything it points at is believed.
	ErrDotGitLink = errors.New("the working tree's .git is a symbolic link")

	// ErrUnreadableSpelling reports a path component whose on-disk spelling
	// could not be established; see canonical.
	ErrUnreadableSpelling = errors.New("cannot read the on-disk spelling")
)

// reservedSegments are the names the store uses INSIDE a key's directory: the
// scope directory (cli.ScopeRepo) and the store's own data directories
// (store.DataDirs). A location-derived key may not contain one, or a
// repository at <base>/local/OS/repo/items would get a store nested inside
// local/OS's scope data. internal/cli's tests pin that this list covers both.
//
// What it prevents is narrow: one LOCATION key's store inside another
// location key's scope data. It does not apply to origin keys, so an origin
// key can still nest inside a location key's directory (an origin-less
// <base>/github.com/acme beside a clone of github.com/acme/repo), as origin
// keys of different depth could already nest inside each other. Those share
// a directory tree and stay inert, since the store reads only regular files
// in items/. Keeping the scope directory out of every key would need a name
// no key segment can take, which changes the store layout (docs/HANDOFF.md).
var reservedSegments = []string{"repo", "items", "order", "tmp"}

// IsReservedSegment reports whether segment is a name the store uses inside a
// key's directory, and so may not appear in a location-derived key.
func IsReservedSegment(segment string) bool {
	return slices.Contains(reservedSegments, segment)
}

// HasRemote reports whether dir's repository has a remote with exactly this
// name. It exists to tell "no origin at all" from "an origin git cannot give a
// URL for": only the first may fall back to a location-derived key, because
// the second is a repository that DOES name its upstream, just not usably.
func HasRemote(ctx context.Context, dir, name string) (bool, error) {
	out, err := git(ctx, dir, ErrNotARepository, "remote")
	if err != nil {
		return false, err
	}

	return slices.Contains(strings.Fields(out), name), nil
}

// fileSystem is the part of the filesystem the key derivation reads. It is a
// seam for one reason: a case-insensitive or normalization-insensitive volume
// (APFS by default) cannot be reproduced on the Linux CI runs, so the tests
// supply ones that fold.
type fileSystem interface {
	Lstat(path string) (fs.FileInfo, error)
	Stat(path string) (fs.FileInfo, error)
	ReadDirNames(path string) ([]string, error)
	// ReadRegular reads path only if it is a regular file, reached without a
	// link in its last component, of at most limit bytes; see
	// boundedread.Regular.
	ReadRegular(path string, limit int64) ([]byte, error)
	EvalSymlinks(path string) (string, error)
	SameFile(a, b fs.FileInfo) bool
}

// osFS is the real filesystem.
type osFS struct{}

func (osFS) Lstat(path string) (fs.FileInfo, error) { return os.Lstat(path) }

func (osFS) Stat(path string) (fs.FileInfo, error) { return os.Stat(path) }

func (osFS) ReadRegular(path string, limit int64) ([]byte, error) {
	return boundedread.Regular(
		func(flag int) (*os.File, error) { return os.OpenFile(path, flag, 0) }, //nolint:gosec // a path git named
		func() (fs.FileInfo, error) { return os.Lstat(path) },
		limit)
}

func (osFS) EvalSymlinks(path string) (string, error) { return filepath.EvalSymlinks(path) }

func (osFS) SameFile(a, b fs.FileInfo) bool { return os.SameFile(a, b) }

func (osFS) ReadDirNames(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	return names, nil
}

// canonical returns path absolute, with every symbolic link resolved, and
// with each component spelled as the directory entry on disk spells it.
//
// The spelling is the part filepath.EvalSymlinks does not do. On a
// case-insensitive volume it keeps whatever case was TYPED, measured on the
// operator's Mac: EvalSymlinks("$T/SRC/LOCAL/os") returns "$T/SRC/LOCAL/os"
// for a directory stored as "$T/src/local/OS", while git reports the stored
// spelling. A key taken from the typed spelling would name a second store for
// the same repository, and a string comparison with git's answer would fail.
//
// It FAILS CLOSED: a component whose stored spelling cannot be established is
// an error, never kept as typed, because on APFS the typed spelling is exactly
// the one that opens a second store. See onDisk for when a directory has to be
// read at all.
func canonical(fsys fileSystem, path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}

	resolved, err := fsys.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}

	volume := filepath.VolumeName(resolved)
	current := volume + string(filepath.Separator)

	for component := range strings.SplitSeq(strings.TrimPrefix(resolved[len(volume):], string(filepath.Separator)),
		string(filepath.Separator)) {
		if component == "" {
			continue
		}

		spelled, err := onDisk(fsys, current, component)
		if err != nil {
			return "", err
		}

		current = filepath.Join(current, spelled)
	}

	return current, nil
}

// onDisk returns the entry of dir that component names, as dir spells it.
//
// Reading dir is avoided when it cannot matter. An ASCII component whose
// case-flipped spelling is NOT the same file lives in a directory that tells
// case apart, and there the typed spelling, which exists, is the stored one:
// Linux and case-sensitive volumes never read a directory, so a directory
// with execute but no read permission still resolves. Otherwise, on a volume
// that folds case, or for a non-ASCII component that normalization could
// fold, the parent is read and the entry that is the SAME FILE is taken,
// decided by the filesystem rather than by folding strings. That also covers
// APFS's Unicode normalization: a name typed NFC and stored NFD is one file.
//
// Every failure along the way is an error: an unreadable parent, a failed
// Lstat, or no entry that matches.
func onDisk(fsys fileSystem, dir, component string) (string, error) {
	path := filepath.Join(dir, component)

	want, err := fsys.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", path, err)
	}

	if isASCII(component) {
		flipped := flipCase(component)
		if flipped == component {
			return component, nil
		}

		other, err := fsys.Lstat(filepath.Join(dir, flipped))

		switch {
		case errors.Is(err, fs.ErrNotExist), err == nil && !fsys.SameFile(want, other):
			return component, nil
		case err != nil:
			return "", fmt.Errorf("%w of %s: %w", ErrUnreadableSpelling, path, err)
		}
	}

	names, err := fsys.ReadDirNames(dir)
	if err != nil {
		return "", fmt.Errorf("%w of %s: %w", ErrUnreadableSpelling, path, err)
	}

	if slices.Contains(names, component) {
		return component, nil
	}

	for _, name := range names {
		have, err := fsys.Lstat(filepath.Join(dir, name))
		if err == nil && fsys.SameFile(want, have) {
			return name, nil
		}
	}

	return "", fmt.Errorf("%w of %s: no entry of %s is that file", ErrUnreadableSpelling, path, dir)
}

func isASCII(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool { return r > unicode.MaxASCII })
}

// flipCase swaps the case of every ASCII letter.
func flipCase(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r - ('a' - 'A')
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return r
		}
	}, s)
}

// Toplevel returns the working tree dir belongs to, found on the FILESYSTEM:
// the nearest directory, from dir upwards, that holds a .git entry (a
// directory, or the file a linked worktree or a submodule has). The result is
// canonical: symbolic links resolved and every component in its on-disk
// spelling.
//
// git is asked too, and must name the SAME directory. Asking only git would
// let the repository choose its own answer: core.worktree in .git/config,
// which anything with write access to the tree can set, moves `rev-parse
// --show-toplevel` to any directory, and a key derived from that would address
// another repository's store. Walking only the filesystem would key a
// directory git does not consider a repository at all.
//
// "Same" is decided by file identity (os.SameFile), never by comparing
// strings: on a case-insensitive volume git answers in the stored spelling
// and the walk starts from the typed one, and the two are one directory.
func Toplevel(ctx context.Context, dir string) (string, error) {
	gitTop, err := Root(ctx, dir)
	if err != nil {
		return "", err
	}

	return toplevel(osFS{}, dir, gitTop)
}

// toplevel is Toplevel after git has answered gitTop.
func toplevel(fsys fileSystem, dir, gitTop string) (string, error) {
	start, err := canonical(fsys, dir)
	if err != nil {
		return "", err
	}

	fsTop, err := dotGitAbove(fsys, start)
	if err != nil {
		return "", err
	}

	walked, err := fsys.Stat(fsTop)
	if err != nil {
		return "", fmt.Errorf("resolving the working tree %s: %w", fsTop, err)
	}

	reported, err := fsys.Stat(gitTop)
	if err != nil {
		return "", fmt.Errorf("resolving the working tree %s: %w", gitTop, err)
	}

	if !fsys.SameFile(walked, reported) {
		return "", fmt.Errorf("%w: its .git is in %s, but git reports the working tree as %s",
			ErrWorktreeElsewhere, fsTop, gitTop)
	}

	return fsTop, nil
}

// dotGitAbove walks from dir, already canonical, to the nearest ancestor that
// holds a .git entry.
func dotGitAbove(fsys fileSystem, dir string) (string, error) {
	for current := dir; ; {
		_, err := fsys.Lstat(filepath.Join(current, ".git"))
		if err == nil {
			return current, nil
		}

		if !errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("looking for .git in %s: %w", current, err)
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("%w: no .git in %s or above it", ErrNotARepository, dir)
		}

		current = parent
	}
}

// maxGitdirRecord bounds a linked worktree's gitdir record, which holds one
// absolute path and a newline. 4096 bytes is Linux's PATH_MAX, the longer of
// the two systems' (macOS's is 1024), so no record git writes comes near it.
const maxGitdirRecord = 4096

// KeyTree returns the working tree an origin-less repository is keyed by:
// its Toplevel, except for a LINKED worktree, which is keyed by its main
// working tree, so every worktree of one repository reads one list
// (operator decision, 2026-09-28).
//
// A linked worktree's .git is a file naming <common>/worktrees/<name>, and
// that directory's gitdir file names the worktree's .git file back. Both are
// repository content, so the link is believed only when it checks out in
// BOTH directions, by file identity: git's per-worktree directory sits in
// the common directory's worktrees/, its gitdir file is this tree's .git, and
// the common directory is the .git of the directory it is taken to be the
// main tree of. A forged gitdir or commondir fails one of the three and is
// refused, never keyed by the worktree's own path instead.
//
// No link is followed where one could stand in for the real thing: a .git
// that is a link is refused outright (ErrDotGitLink), every identity is
// compared by Lstat, a worktree's .git must be a regular file and a main
// tree's a real directory, and the gitdir record is read as a small regular
// file only (maxGitdirRecord), since a sandbox can write it and the host
// build reads it.
func KeyTree(ctx context.Context, dir string) (string, error) {
	top, err := Toplevel(ctx, dir)
	if err != nil {
		return "", err
	}

	gitDir, err := git(ctx, dir, ErrNotARepository, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}

	common, err := CommonDir(ctx, dir)
	if err != nil {
		return "", err
	}

	return keyTree(osFS{}, top, gitDir, common)
}

// keyTree is KeyTree after git has answered.
func keyTree(fsys fileSystem, top, gitDir, common string) (string, error) {
	dotGit := filepath.Join(top, ".git")

	mine, err := fsys.Lstat(dotGit)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", dotGit, err)
	}

	if mine.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("%w: %s", ErrDotGitLink, dotGit)
	}

	own, err := fsys.Lstat(gitDir)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", gitDir, err)
	}

	shared, err := fsys.Lstat(common)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", common, err)
	}

	// The main working tree, or a submodule: its git directory IS the common
	// one, and the tree is keyed by where it is.
	if fsys.SameFile(own, shared) {
		return top, nil
	}

	return linkedMain(fsys, top, gitDir, common, mine, shared)
}

// linkedMain is keyTree for a linked worktree: the three checks, then the
// main tree. mine is top/.git and shared is common, both by Lstat.
func linkedMain(fsys fileSystem, top, gitDir, common string, mine, shared fs.FileInfo) (string, error) {
	unverified := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s: %s", ErrWorktreeUnverified, top, fmt.Sprintf(format, args...))
	}

	dotGit := filepath.Join(top, ".git")
	if !mine.Mode().IsRegular() {
		return "", unverified("%s is not the file a linked worktree has", quoted(dotGit))
	}

	if err := sameEntry(fsys, filepath.Dir(gitDir), filepath.Join(common, "worktrees")); err != nil {
		return "", unverified("its git directory %s is not in %s (%s)",
			quoted(gitDir), quoted(filepath.Join(common, "worktrees")), why(err))
	}

	record := filepath.Join(gitDir, "gitdir")

	back, err := fsys.ReadRegular(record, maxGitdirRecord)
	if err != nil {
		return "", unverified("reading %s: %s", quoted(record), why(err))
	}

	pointer := strings.TrimSpace(string(back))
	if !filepath.IsAbs(pointer) {
		pointer = filepath.Join(gitDir, pointer)
	}

	if err := sameEntry(fsys, pointer, dotGit); err != nil {
		return "", unverified("%s names %s, not this tree's .git (%s)", quoted(record), quoted(pointer), why(err))
	}

	main := filepath.Dir(common)

	if info, err := fsys.Lstat(filepath.Join(main, ".git")); err != nil || !info.IsDir() || !fsys.SameFile(info, shared) {
		return "", unverified("%s is not the .git directory of a working tree, so this worktree has no "+
			"main working tree to be keyed by (a bare repository, or one made with --separate-git-dir); "+
			"give the repository an origin remote to key it by that", quoted(common))
	}

	return canonical(fsys, main)
}

// sameEntry is nil when a and b are one file, compared WITHOUT following a
// link in either last component, and says why not otherwise. A link is never
// the same file as its target, so a link cannot stand in for the real entry.
func sameEntry(fsys fileSystem, a, b string) error {
	first, err := fsys.Lstat(a)
	if err != nil {
		return err
	}

	second, err := fsys.Lstat(b)
	if err != nil {
		return err
	}

	if !fsys.SameFile(first, second) {
		return errors.New("different files")
	}

	return nil
}

// why is err without the path a *fs.PathError repeats: the path is one the
// caller already quoted, and repeating it raw would print what quoting kept
// off the terminal.
func why(err error) string {
	if pathErr, ok := errors.AsType[*fs.PathError](err); ok {
		return pathErr.Op + ": " + pathErr.Err.Error()
	}

	return err.Error()
}

// maxQuoted bounds how much of a repository-supplied string an error repeats.
const maxQuoted = 128

// quoted renders s, a path that git read from, or that is, repository
// content anything able to write the repository controls, safely for a terminal: Go-quoted, so an escape
// sequence is printed as text, never interpreted, and truncated.
func quoted(s string) string {
	if len(s) <= maxQuoted {
		return strconv.Quote(s)
	}

	return strconv.Quote(s[:maxQuoted]) + "..."
}

// PathUnder derives an origin-less repository's key: the location of its
// working tree toplevel relative to base, the root `canga git clone` lays
// repositories out under. It agrees with Path by construction for a tree
// sitting where `canga git clone` would have put its origin, which is what
// makes ~/src/local/OS key as "local/OS" and an origin-less
// ~/src/github.com/acme/widget key as "github.com/acme/widget". Like Path's,
// the result is a label for a list, not an authenticated identity.
//
// Both paths are made canonical before they are compared: symbolic links
// resolved, so a link is another name for the same key rather than a second
// key and a link inside base that points out of it is outside; and each
// component in its on-disk spelling, so on a case-insensitive volume a
// wrongly-cased base or tree still yields the key the stored spelling does.
// The comparison is by path segment, never by string prefix: /src2 is not
// below /src.
//
// The result uses forward slashes and keeps the directory's own case, like
// Path's; a caller using it as a directory name passes it through EscapePath.
func PathUnder(base, toplevel string) (string, error) {
	return pathUnder(osFS{}, base, toplevel)
}

// pathUnder is PathUnder on a given filesystem.
func pathUnder(fsys fileSystem, base, top string) (string, error) {
	realBase, err := canonical(fsys, base)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: %s%s", ErrNoSuchBase, base, setBy())
		}

		return "", fmt.Errorf("resolving the base directory %s%s: %w", base, setBy(), err)
	}

	realTop, err := canonical(fsys, top)
	if err != nil {
		return "", fmt.Errorf("resolving the working tree %s: %w", top, err)
	}

	rel, err := filepath.Rel(realBase, realTop)
	if err != nil || filepath.IsAbs(rel) || rel == ".." ||
		strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w %s%s: %s is outside it", ErrNotUnderBase, realBase, setBy(), realTop)
	}

	if rel == "." {
		return "", fmt.Errorf("%w %s: it is the base directory itself", ErrNotUnderBase, realTop)
	}

	key := filepath.ToSlash(rel)

	for segment := range strings.SplitSeq(key, "/") {
		if !IsSafeSegment(segment) {
			return "", fmt.Errorf("%w: refusing path segment %q in %s", ErrBadPath, segment, realTop)
		}

		if IsReservedSegment(segment) {
			return "", fmt.Errorf("%w: %q is a name the reminder store uses inside a key, in %s",
				ErrBadPath, segment, realTop)
		}
	}

	return key, nil
}

// setBy names the variable the base came from, when it did come from it.
// Blaming CANGA_SRC_DIR for a base it did not set sends the reader to fix the
// wrong thing.
func setBy() string {
	if os.Getenv(BaseDirVar) == "" {
		return ""
	}

	return " (set by " + BaseDirVar + ")"
}
