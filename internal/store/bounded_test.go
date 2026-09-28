// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/brunovenceslau/canga/internal/boundedread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// within fails the test instead of hanging it: a FIFO read without
// O_NONBLOCK never returns, which is the failure these tests exist for.
func within[T any](t *testing.T, what string, call func() (T, error)) (T, error) {
	t.Helper()

	type result struct {
		value T
		err   error
	}

	done := make(chan result, 1)

	go func() {
		value, err := call()
		done <- result{value, err}
	}()

	select {
	case r := <-done:
		return r.value, r.err
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not return", what)

		var zero T

		return zero, nil
	}
}

// planted names the non-regular files a sandbox could put where the host
// build reads an item or an order document, each made at path.
var planted = map[string]func(t *testing.T, path string){
	"a FIFO": func(t *testing.T, path string) {
		t.Helper()
		require.NoError(t, syscall.Mkfifo(path, uint32(filePerm)))
	},
	"a link to a file outside the store": func(t *testing.T, path string) {
		t.Helper()

		secret := filepath.Join(t.TempDir(), "secret")
		require.NoError(t, os.WriteFile(secret, []byte("TOPSECRET\n"), filePerm))
		require.NoError(t, os.Symlink(secret, path))
	},
	"a link to another item": func(t *testing.T, path string) {
		t.Helper()

		// Relative, so it stays inside the store: os.Root would follow it.
		other := "20260101T000000.000000Z-00000000" + itemExt
		require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), other), []byte("another item\n"), filePerm))
		require.NoError(t, os.Symlink(other, path))
	},
}

// TestStore_ReadsOnlySmallRegularItems: an item is read only as a regular
// file of at most maxItemBytes, never through a link, and never blocking
// (ship-gate round 2, security-auditor Low: a FIFO item hung `list` with
// Ctrl-C ignored).
func TestStore_ReadsOnlySmallRegularItems(t *testing.T) {
	t.Parallel()

	const id = "20260102T000000.000000Z-0000000a"

	for name, plant := range planted {
		t.Run("Get refuses "+name, func(t *testing.T) {
			t.Parallel()

			reminders := newTestStore(t)
			plant(t, filepath.Join(reminders.Dir(), itemsDir, id+itemExt))

			got, err := within(t, "Get", func() (Item, error) { return reminders.Get(id) })
			require.ErrorIs(t, err, boundedread.ErrNotRegular)
			assert.NotContains(t, got.Text, "TOPSECRET")
		})

		t.Run("List skips "+name, func(t *testing.T) {
			t.Parallel()

			reminders := newTestStore(t)
			kept, err := reminders.Add("real")
			require.NoError(t, err)

			plant(t, filepath.Join(reminders.Dir(), itemsDir, id+itemExt))

			items, err := within(t, "List", reminders.List)
			require.NoError(t, err)

			ids := make([]string, 0, len(items))
			for _, item := range items {
				ids = append(ids, item.ID)
				assert.NotContains(t, item.Text, "TOPSECRET")
			}

			assert.Contains(t, ids, kept.ID)
			assert.NotContains(t, ids, id)
		})
	}

	t.Run("an oversized item fails Get and List", func(t *testing.T) {
		t.Parallel()

		reminders := newTestStore(t)
		require.NoError(t, writeItemFile(reminders, id, strings.Repeat("x", maxItemBytes+1)))

		_, err := reminders.Get(id)
		require.ErrorIs(t, err, boundedread.ErrTooLarge)

		_, err = reminders.List()
		require.ErrorIs(t, err, boundedread.ErrTooLarge)
	})

	t.Run("an item at the limit is read", func(t *testing.T) {
		t.Parallel()

		reminders := newTestStore(t)
		require.NoError(t, writeItemFile(reminders, id, strings.Repeat("x", maxItemBytes)))

		_, err := reminders.Get(id)
		require.NoError(t, err)
	})

	// What Add writes, the store can always read back.
	t.Run("Add refuses text it could not read back", func(t *testing.T) {
		t.Parallel()

		reminders := newTestStore(t)

		_, err := reminders.Add(strings.Repeat("x", maxItemBytes))
		require.ErrorIs(t, err, ErrTextTooLarge)
	})
}

// TestStore_ReadsOnlySmallRegularOrder: the order document is read the same
// way.
func TestStore_ReadsOnlySmallRegularOrder(t *testing.T) {
	t.Parallel()

	for name, plant := range planted {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reminders := newTestStore(t)
			_, err := reminders.Add("real")
			require.NoError(t, err)

			plant(t, filepath.Join(reminders.Dir(), filepath.FromSlash(orderFile(1))))

			_, err = within(t, "List", reminders.List)
			require.ErrorIs(t, err, boundedread.ErrNotRegular)
		})
	}

	t.Run("an oversized order document", func(t *testing.T) {
		t.Parallel()

		reminders := newTestStore(t)
		require.NoError(t, os.WriteFile(filepath.Join(reminders.Dir(), filepath.FromSlash(orderFile(1))),
			[]byte(strings.Repeat("x", maxOrderBytes+1)), filePerm))

		_, err := reminders.List()
		require.ErrorIs(t, err, boundedread.ErrTooLarge)
	})
}

// TestOpen_ReportsIOFailures: every plain I/O failure on the way down to the
// store directory is an error that says what failed, never a store opened
// somewhere else and never a silent empty one (ship-gate round 2,
// test-engineer Low: these branches had no coverage).
func TestOpen_ReportsIOFailures(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission bits these failures come from")
	}

	// lock sets mode on path and restores it when the test ends, so
	// t.TempDir's cleanup can remove what is below.
	lock := func(t *testing.T, path string, mode os.FileMode) {
		t.Helper()
		require.NoError(t, os.Chmod(path, mode))
		t.Cleanup(func() { _ = os.Chmod(path, dirPerm) })
	}

	layout := func(t *testing.T) (base string, cfg Config) {
		t.Helper()

		base = t.TempDir()

		return base, Config{Base: base, Dir: filepath.Join(base, "a", "b", "store")}
	}

	t.Run("a directory the parent will not hold", func(t *testing.T) {
		t.Parallel()

		base, cfg := layout(t)
		lock(t, base, 0o500)

		_, err := Open(cfg)
		require.ErrorIs(t, err, os.ErrPermission)
		assert.Contains(t, err.Error(), "create store")
	})

	t.Run("a directory that cannot be searched", func(t *testing.T) {
		t.Parallel()

		base, cfg := layout(t)
		require.NoError(t, os.MkdirAll(filepath.Join(base, "a", "b"), dirPerm))
		lock(t, filepath.Join(base, "a"), 0o400)

		_, err := Open(cfg)
		require.ErrorIs(t, err, os.ErrPermission)
	})

	// A lookup that fails for any reason but absence: a name longer than any
	// directory entry can be (NAME_MAX is 255 on Linux and on macOS).
	t.Run("a name no directory can look up", func(t *testing.T) {
		t.Parallel()

		base := t.TempDir()

		_, err := Open(Config{Base: base, Dir: filepath.Join(base, strings.Repeat("n", 300), "store")})
		require.ErrorIs(t, err, syscall.ENAMETOOLONG)
		assert.Contains(t, err.Error(), "open store")
	})

	t.Run("a directory that will not open", func(t *testing.T) {
		t.Parallel()

		base, cfg := layout(t)
		require.NoError(t, os.MkdirAll(filepath.Join(base, "a"), dirPerm))
		lock(t, filepath.Join(base, "a"), 0o000)

		_, err := OpenExisting(cfg)
		require.ErrorIs(t, err, os.ErrPermission)
	})

	for name, target := range map[string]func(cfg Config) string{
		// After the open, the parent stops answering for the name.
		"the parent, after the open": func(cfg Config) string { return filepath.Dir(cfg.Dir) },
		// After the open, the opened directory stops answering for itself.
		"the directory, after the open": func(cfg Config) string { return cfg.Dir },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, cfg := layout(t)
			require.NoError(t, os.MkdirAll(cfg.Dir, dirPerm))

			locked := target(cfg)

			t.Cleanup(func() { _ = os.Chmod(locked, dirPerm) })

			var chmodErr error

			_, err := OpenExisting(cfg, func(s *Store) {
				s.hookAfterOpenRoot = func() { chmodErr = os.Chmod(locked, 0o000) }
			})

			require.NoError(t, chmodErr)
			require.ErrorIs(t, err, os.ErrPermission)
			assert.Contains(t, err.Error(), "open store")
		})
	}
}
