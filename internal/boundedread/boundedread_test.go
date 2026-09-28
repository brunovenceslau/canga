// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package boundedread

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fileAt opens path the way a caller of Regular does, and re-reads it by name
// without following a link.
func fileAt(path string) (Opener, func() (fs.FileInfo, error)) {
	return func(flag int) (*os.File, error) { return os.OpenFile(path, flag, 0) },
		func() (fs.FileInfo, error) { return os.Lstat(path) }
}

// readWithin fails the test instead of hanging it: the FIFO case is exactly
// a read that never returns when O_NONBLOCK is missing.
func readWithin(t *testing.T, path string, limit int64) ([]byte, error) {
	t.Helper()

	type result struct {
		data []byte
		err  error
	}

	done := make(chan result, 1)

	go func() {
		open, lstat := fileAt(path)

		data, err := Regular(open, lstat, limit)
		done <- result{data, err}
	}()

	select {
	case r := <-done:
		return r.data, r.err
	case <-time.After(10 * time.Second):
		t.Fatalf("reading %s did not return", path)

		return nil, nil
	}
}

func TestRegular(t *testing.T) {
	t.Parallel()

	t.Run("a regular file within the limit is read whole", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "f")
		require.NoError(t, os.WriteFile(path, []byte("12345"), 0o600))

		data, err := readWithin(t, path, 5)
		require.NoError(t, err)
		assert.Equal(t, "12345", string(data))
	})

	t.Run("one byte over the limit is refused", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "f")
		require.NoError(t, os.WriteFile(path, []byte("123456"), 0o600))

		_, err := readWithin(t, path, 5)
		require.ErrorIs(t, err, ErrTooLarge)
	})

	t.Run("a FIFO is refused without blocking", func(t *testing.T) {
		t.Parallel()

		path := filepath.Join(t.TempDir(), "fifo")
		require.NoError(t, syscall.Mkfifo(path, 0o600))

		_, err := readWithin(t, path, 5)
		require.ErrorIs(t, err, ErrNotRegular)
	})

	t.Run("a symbolic link is refused, even to a regular file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		require.NoError(t, os.WriteFile(target, []byte("secret"), 0o600))
		require.NoError(t, os.Symlink(target, filepath.Join(dir, "link")))

		data, err := readWithin(t, filepath.Join(dir, "link"), 64)
		require.ErrorIs(t, err, ErrNotRegular)
		assert.Empty(t, data)
	})

	t.Run("a directory is refused", func(t *testing.T) {
		t.Parallel()

		_, err := readWithin(t, t.TempDir(), 64)
		require.ErrorIs(t, err, ErrNotRegular)
	})

	t.Run("a missing file keeps fs.ErrNotExist", func(t *testing.T) {
		t.Parallel()

		_, err := readWithin(t, filepath.Join(t.TempDir(), "missing"), 64)
		require.ErrorIs(t, err, fs.ErrNotExist)
	})

	// An opener that follows links (os.Root does, for a link that stays
	// inside it) is caught by the re-read by name: the name is a link now, or
	// it is another file than the one opened.
	t.Run("a following opener is caught by the name check", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		target := filepath.Join(dir, "target")
		link := filepath.Join(dir, "link")

		require.NoError(t, os.WriteFile(target, []byte("other"), 0o600))
		require.NoError(t, os.Symlink(target, link))

		following := func(int) (*os.File, error) { return os.Open(link) }

		_, err := Regular(following, func() (fs.FileInfo, error) { return os.Lstat(link) }, 64)
		require.ErrorIs(t, err, ErrNotRegular)

		// The name swapped for another file after the open.
		other := filepath.Join(dir, "other")
		require.NoError(t, os.WriteFile(other, []byte("x"), 0o600))

		_, err = Regular(following, func() (fs.FileInfo, error) { return os.Lstat(other) }, 64)
		require.ErrorIs(t, err, ErrNotRegular)
	})
}
