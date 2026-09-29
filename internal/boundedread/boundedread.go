// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package boundedread reads a small file that something else can write, so
// that what is there cannot hang the reader, exhaust its memory, or carry the
// read to another file.
//
// Both builds read such files. The host build reads files a sandbox can write
// (the reminder store, and a linked worktree's gitdir record), and a plain
// os.ReadFile of one would block forever on a FIFO, read /dev/zero until the
// process dies, and follow a link to any file the host user can read.
package boundedread

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
)

// Flags are the flags an Opener is handed: read only, never follow a link in
// the last component, and never block in the open (a FIFO with no writer
// would otherwise wait for one forever).
const Flags = os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK

var (
	// ErrNotRegular reports a name that is not a regular file: a link, a
	// FIFO, a device, a socket or a directory, or a file replaced under the
	// name while it was being opened.
	ErrNotRegular = errors.New("not a regular file")

	// ErrTooLarge reports a regular file larger than the reader's limit.
	ErrTooLarge = errors.New("file larger than the limit")
)

// Opener opens the file with the flags given, which are always Flags.
type Opener func(flag int) (*os.File, error)

// Regular reads the file open opens, only if it is a regular file of at most
// limit bytes.
//
// The file is judged by the OPEN handle (fstat), not by a lookup before it,
// so nothing can be swapped in between. lstat, when not nil, re-reads the
// name without following a link after the open, and the two must be the same
// file: an opener that follows links anyway (os.Root follows one that stays
// inside it) is caught there, as is a name replaced after the open.
func Regular(open Opener, lstat func() (fs.FileInfo, error), limit int64) ([]byte, error) {
	file, err := open(Flags)
	if err != nil {
		// O_NOFOLLOW's answer to a link in the last component, or an opener
		// that refuses the link's target instead (os.Root refuses one that
		// leaves it): either way the name is a link, and that is the answer.
		if errors.Is(err, syscall.ELOOP) || isLink(lstat) {
			return nil, fmt.Errorf("%w: a symbolic link", ErrNotRegular)
		}

		return nil, err
	}

	defer func() { _ = file.Close() }()

	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}

	if !opened.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s", ErrNotRegular, opened.Mode().Type())
	}

	if lstat != nil {
		named, err := lstat()
		if err != nil {
			return nil, err
		}

		if named.Mode()&fs.ModeSymlink != 0 || !os.SameFile(opened, named) {
			return nil, fmt.Errorf("%w: the name is a link, or was replaced while it was read", ErrNotRegular)
		}
	}

	// One byte past the limit tells "exactly the limit" from "more".
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}

	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w of %d bytes", ErrTooLarge, limit)
	}

	return data, nil
}

// isLink reports whether lstat, when there is one, finds a link.
func isLink(lstat func() (fs.FileInfo, error)) bool {
	if lstat == nil {
		return false
	}

	info, err := lstat()

	return err == nil && info.Mode()&fs.ModeSymlink != 0
}
