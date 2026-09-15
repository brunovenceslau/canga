// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewID(t *testing.T) {
	t.Parallel()

	shape := regexp.MustCompile(`^\d{8}T\d{6}\.\d{6}Z-[0-9a-f]{8}$`)

	seen := make(map[string]bool, 1000)

	for range 1000 {
		id := newID()
		require.Regexp(t, shape, id)
		require.NoError(t, checkID(id))
		assert.Falsef(t, seen[id], "newID repeated %s", id)
		seen[id] = true
	}
}

func TestCheckID(t *testing.T) {
	t.Parallel()

	valid := []string{
		"20260915T142233Z-9f3a1c",
		"a",
		"my-note",
		"note.2",
		"A_B",
	}

	for _, id := range valid {
		require.NoErrorf(t, checkID(id), "id %q", id)
	}

	// Every one of these would address something other than one file in the
	// items directory. os.Root refuses them too; checkID refuses them with a
	// message that names the argument at fault.
	invalid := []string{
		"", ".", "..", "../escape", "items/../../escape", "a/b", `a\b`,
		".hidden", "-leading", "a b", "a\x00b", "a\nb",
	}

	for _, id := range invalid {
		require.ErrorIsf(t, checkID(id), ErrInvalidID, "id %q", id)
	}
}

func TestRandomName(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool, 1000)

	for range 1000 {
		name := randomName()
		assert.Len(t, name, 16)
		assert.Falsef(t, seen[name], "randomName repeated %s", name)
		seen[name] = true
	}
}

// writeItemFile drops a well-formed item file into a store, bypassing Add.
func writeItemFile(reminders *Store, id, body string) error {
	return writeRaw(reminders, id+itemExt, body)
}

// writeRaw drops any file into a store's items directory.
func writeRaw(reminders *Store, name, body string) error {
	return os.WriteFile(filepath.Join(reminders.Dir(), itemsDir, name), []byte(body), filePerm)
}
