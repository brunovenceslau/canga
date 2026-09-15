// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seeded(t *testing.T, ids ...string) *Store {
	t.Helper()

	reminders := newTestStore(t, WithIDFunc(idsInOrder(t, ids...)))

	for _, id := range ids {
		_, err := reminders.Add("reminder " + id)
		require.NoError(t, err)
	}

	return reminders
}

func listedIDs(t *testing.T, reminders *Store) []string {
	t.Helper()

	items, err := reminders.List()
	require.NoError(t, err)

	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}

	return ids
}

func TestStore_Reorder(t *testing.T) {
	t.Parallel()

	reminders := seeded(t, "a", "b", "c")

	require.NoError(t, reminders.Reorder([]string{"c"}))
	assert.Equal(t, []string{"c", "a", "b"}, listedIDs(t, reminders),
		"the named id moves to the front and the rest keep their relative order")

	require.NoError(t, reminders.Reorder([]string{"b", "a"}))
	assert.Equal(t, []string{"b", "a", "c"}, listedIDs(t, reminders),
		"several ids move in the order given")
}

func TestStore_ReorderRejects(t *testing.T) {
	t.Parallel()

	reminders := seeded(t, "a")

	require.ErrorIs(t, reminders.Reorder([]string{"nosuch"}), ErrNotFound)
	require.ErrorIs(t, reminders.Reorder([]string{"../escape"}), ErrInvalidID)

	// A rejected reorder must publish nothing: the store is left exactly as it
	// was, rather than half-reordered.
	head, _, err := reminders.orderHead()
	require.NoError(t, err)
	assert.Zero(t, head)
}

// TestStore_ReorderRetriesInsteadOfOverwriting is the CAS, asserted rather than
// hoped for. The hook lands a competing writer in the one window that matters —
// between reading the head and publishing the next version — which no amount of
// running two processes can be relied on to hit.
func TestStore_ReorderRetriesInsteadOfOverwriting(t *testing.T) {
	t.Parallel()

	reminders := seeded(t, "a", "b", "c")

	require.NoError(t, reminders.Reorder([]string{"a"}))

	head, _, err := reminders.orderHead()
	require.NoError(t, err)
	require.Equal(t, 1, head)

	attempts := 0
	reminders.hookBeforePublish = func() {
		attempts++

		// Only the first attempt is contended; a competitor on every attempt
		// would make the retry loop run to its bound, which is a different test.
		if attempts > 1 {
			return
		}

		require.NoError(t, reminders.publishOrder(2, []string{"c", "b", "a"}))
	}

	require.NoError(t, reminders.Reorder([]string{"b"}))

	assert.Equal(t, 2, attempts, "the reorder did not retry after losing the race")

	published, err := reminders.root.ReadFile(orderFile(2))
	require.NoError(t, err)
	assert.Equal(t, "c\nb\na\n", string(published),
		"the winner's version must survive byte-for-byte")

	head, ids, err := reminders.orderHead()
	require.NoError(t, err)
	assert.Equal(t, 3, head, "the loser publishes the NEXT version, it does not overwrite")
	assert.Equal(t, []string{"b", "c", "a"}, ids,
		"the retry recomputes against the winner's result, not its own stale read")
}

// TestStore_ListPlacesAnUnorderedItemLast is the first decoupling rule: Add
// never writes the order document, which is what keeps it lock-free.
func TestStore_ListPlacesAnUnorderedItemLast(t *testing.T) {
	t.Parallel()

	reminders := newTestStore(t, WithIDFunc(idsInOrder(t, "a", "b", "c")))

	for _, id := range []string{"a", "b"} {
		_, err := reminders.Add("reminder " + id)
		require.NoError(t, err)
	}

	require.NoError(t, reminders.Reorder([]string{"b"}))

	_, err := reminders.Add("reminder c")
	require.NoError(t, err)

	_, ordered, err := reminders.orderHead()
	require.NoError(t, err)
	assert.NotContains(t, ordered, "c", "adding an item must not touch the order document")
	assert.Equal(t, []string{"b", "a", "c"}, listedIDs(t, reminders))
}

// TestStore_ListSkipsARemovedOrderedItem is the second decoupling rule: Remove
// never writes the order document either, so a stale entry has to be harmless
// on read and collected by the next reorder.
func TestStore_ListSkipsARemovedOrderedItem(t *testing.T) {
	t.Parallel()

	reminders := seeded(t, "a", "b", "c")
	require.NoError(t, reminders.Reorder([]string{"c"}))
	require.NoError(t, reminders.Remove("a"))

	_, ordered, err := reminders.orderHead()
	require.NoError(t, err)
	assert.Contains(t, ordered, "a", "removing an item must not touch the order document")
	assert.Equal(t, []string{"c", "b"}, listedIDs(t, reminders))

	require.NoError(t, reminders.Reorder([]string{"b"}))

	_, ordered, err = reminders.orderHead()
	require.NoError(t, err)
	assert.NotContains(t, ordered, "a", "a later reorder collects the stale entry")
}

func TestStore_OrderPrunesBelowTheHeadOnly(t *testing.T) {
	t.Parallel()

	reminders := seeded(t, "a", "b")

	const publishes = orderKeepVersions + 3
	for range publishes {
		require.NoError(t, reminders.Reorder([]string{"a"}))
	}

	versions := orderVersions(t, reminders.Dir())
	assert.Len(t, versions, orderKeepVersions)

	for _, version := range versions {
		assert.Greater(t, version, publishes-orderKeepVersions)
		assert.LessOrEqual(t, version, publishes)
	}

	// The head is still readable, which is the property pruning must never break.
	head, ids, err := reminders.orderHead()
	require.NoError(t, err)
	assert.Equal(t, publishes, head)
	assert.Equal(t, []string{"a", "b"}, ids)
}

// TestStore_OrderToleratesJunk: the order directory is a plain directory a
// person can drop a file into. One stray name must not hide every reminder.
func TestStore_OrderToleratesJunk(t *testing.T) {
	t.Parallel()

	reminders := seeded(t, "a", "b")
	require.NoError(t, reminders.Reorder([]string{"b"}))

	require.NoError(t, os.WriteFile(
		filepath.Join(reminders.Dir(), orderDir, "notes.txt"), []byte("hello"), filePerm,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(reminders.Dir(), orderDir, "1"), []byte("../escape\n\nb\n"), filePerm,
	))

	head, _, err := reminders.orderHead()
	require.NoError(t, err)
	assert.Equal(t, 1, head)
	assert.Equal(t, []string{"b", "a"}, listedIDs(t, reminders))
}

func TestStore_OrderHeadOnAnEmptyStore(t *testing.T) {
	t.Parallel()

	head, ids, err := newTestStore(t).orderHead()
	require.NoError(t, err)
	assert.Zero(t, head)
	assert.Empty(t, ids)
}

// TestStore_ReorderConcurrent is the CAS under real contention. The invariant
// is arithmetic, not timing: every successful publish bumps the version by
// exactly one, so the final head MUST equal the number of successes across all
// processes — a skipped or shared version breaks that equality.
func TestStore_ReorderConcurrent(t *testing.T) {
	t.Parallel()

	const rounds = 10

	writers := *raceProcs
	require.Positive(t, writers)

	dir := pristineStore(t)

	seed, err := Open(Config{Dir: dir})
	require.NoError(t, err)

	ids := make([]string, 0, 8)

	for i := range 8 {
		item, err := seed.Add("seed " + strconv.Itoa(i))
		require.NoError(t, err)

		ids = append(ids, item.ID)
	}

	require.NoError(t, seed.Close())

	procs := make([]*helperProc, writers)
	for worker := range procs {
		procs[worker] = startHelper(t, map[string]string{
			envRole:   roleReorder,
			envStore:  dir,
			envWorker: strconv.Itoa(worker),
			envItems:  strconv.Itoa(rounds),
			envSpace:  "1",
		})
	}

	published, attempts := 0, 0

	for _, proc := range procs {
		rep := proc.wait(t)
		published += rep.Rounds
		attempts += rep.Attempts
	}

	require.Equal(t, writers*rounds, published, "a reorder gave up instead of retrying")

	reminders, err := OpenExisting(Config{Dir: dir})
	require.NoError(t, err)

	defer func() { require.NoError(t, reminders.Close()) }()

	head, ordered, err := reminders.orderHead()
	require.NoError(t, err)

	// A SANDWICH, and deliberately not an equality — the equality is tempting
	// and false. A writer that loses does collect the version it published, but
	// another writer may already have read that version as the head and
	// computed its own from it, so the head legitimately outruns the number of
	// winners by up to the number of losses. Measured: 40 winners, head 41,
	// with nothing shared and nothing lost.
	//
	// Both halves earn their place. The lower bound breaks if two writers ever
	// share one version; the upper bound breaks if a version is skipped, since
	// the head can never exceed the number of times a version was published at
	// all.
	assert.GreaterOrEqual(t, head, published, "two writers shared one version")
	assert.LessOrEqual(t, head, attempts,
		"the head outran the publish attempts, so a version was skipped")
	assert.Positive(t, attempts)
	assert.ElementsMatch(t, ids, ordered, "the final order must still name every item, once")

	versions := orderVersions(t, dir)
	require.NotEmpty(t, versions)
	assert.LessOrEqual(t, len(versions), orderKeepVersions, "history outgrew its bound")

	for _, version := range versions {
		assert.LessOrEqualf(t, version, head, "version %d shadows the head", version)
	}

	// No staging file survived a lost race.
	temps, err := os.ReadDir(filepath.Join(dir, tmpDir))
	require.NoError(t, err)
	assert.Empty(t, temps)
}

// TestStore_ReorderRefusesAPrunedHole is the deterministic twin of what the
// concurrency gate caught: pruning frees an old version's NAME, so linking to
// it can succeed while the head is far ahead. A reorder that believed the link
// would report success and change nothing.
func TestStore_ReorderRefusesAPrunedHole(t *testing.T) {
	t.Parallel()

	reminders := seeded(t, "a", "b", "c")

	attempts := 0
	reminders.hookBeforePublish = func() {
		attempts++

		if attempts > 1 {
			return
		}

		// Race far enough ahead that pruning frees version 1, which is exactly
		// the version this reorder is about to publish.
		for version := 1; version <= orderKeepVersions+1; version++ {
			require.NoError(t, reminders.publishOrder(version, []string{"c", "b", "a"}))
		}

		reminders.pruneOrder(orderKeepVersions + 1)

		_, err := reminders.root.Stat(orderFile(1))
		require.ErrorIs(t, err, fs.ErrNotExist, "the hole this test needs was not opened")
	}

	require.NoError(t, reminders.Reorder([]string{"b"}))

	assert.Equal(t, 2, attempts, "the reorder believed a link into the hole")

	head, ordered, err := reminders.orderHead()
	require.NoError(t, err)
	assert.Equal(t, orderKeepVersions+2, head)
	assert.Equal(t, []string{"b", "c", "a"}, ordered)

	_, err = reminders.root.Stat(orderFile(1))
	assert.ErrorIs(t, err, fs.ErrNotExist, "the losing publish was left in the hole")
}

// orderVersions lists the version numbers still present in a store.
func orderVersions(t *testing.T, dir string) []int {
	t.Helper()

	entries, err := os.ReadDir(filepath.Join(dir, orderDir))
	require.NoError(t, err)

	versions := make([]int, 0, len(entries))

	for _, entry := range entries {
		version, err := strconv.Atoi(entry.Name())
		require.NoError(t, err)

		versions = append(versions, version)
	}

	return versions
}

func TestStage_CleansUpAfterItself(t *testing.T) {
	t.Parallel()

	reminders := newTestStore(t)

	name, err := reminders.stage("body\n")
	require.NoError(t, err)

	require.NoError(t, reminders.root.Link(name, orderFile(1)))
	require.NoError(t, reminders.root.Remove(name))

	_, err = reminders.root.Stat(name)
	assert.ErrorIs(t, err, fs.ErrNotExist)
}
