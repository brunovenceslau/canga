// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Race-gate plumbing.
//
// The store's central claim — that many PROCESSES can write at once without a
// lock and without losing or tearing anything — cannot be tested inside one
// process, and was impossible to test in shell without being either flaky or
// vacuous. So the test binary re-execs ITSELF as helper processes: every helper
// is this same code, built with the same -race, and there is no second binary
// to keep in step with it.
// ---------------------------------------------------------------------------

const (
	envRole   = "CANGA_TEST_ROLE"
	envStore  = "CANGA_TEST_STORE"
	envWorker = "CANGA_TEST_WORKER"
	envItems  = "CANGA_TEST_ITEMS"
	envSpace  = "CANGA_TEST_SPACE"
	envDone   = "CANGA_TEST_DONE"

	roleWriter  = "writer"
	roleReader  = "reader"
	roleReorder = "reorder"

	// itemsPerWriter and maxReaderRounds keep the default gate CI-sized. The
	// full-size soak is the same code with -race-procs raised.
	itemsPerWriter  = 32
	maxReaderRounds = 200_000

	// raceFiller makes an item big enough that a torn write would be visible as
	// a short read rather than hiding inside one disk block.
	raceFiller = 2000
)

var (
	raceProcs = flag.Int("race-procs", 4, "writer processes the store race gates fan out to")

	// raceStoreDir runs the gates against a filesystem of the caller's choosing
	// instead of the default temporary one. It exists because the invariants
	// this store relies on are the FILESYSTEM's, not Go's: link(2) refusing an
	// existing name has been measured on ext4 and APFS, and is still unmeasured
	// across the macOS-to-VM virtiofs boundary a shared store would span.
	raceStoreDir = flag.String("race-store-dir", "",
		"parent directory to build the gates' store under (default: a temporary one)")
)

// report is what a helper process tells its parent. Every field is a COUNT, not
// a duration: the gate asserts invariants, never timing, so it cannot be made
// to pass or fail by how fast the machine happens to be.
type report struct {
	Placed   int `json:"placed"`
	Draws    int `json:"draws"` // id draws; more than Placed means collisions were real
	Reads    int `json:"reads"`
	Torn     int `json:"torn"`
	Rounds   int `json:"rounds"`
	Attempts int `json:"attempts"` // publish attempts, won or lost
}

func TestMain(m *testing.M) {
	if role := os.Getenv(envRole); role != "" {
		os.Exit(runHelper(role))
	}

	os.Exit(m.Run())
}

func runHelper(role string) int {
	var (
		rep report
		err error
	)

	switch role {
	case roleWriter:
		rep, err = runWriter()
	case roleReader:
		rep, err = runReader()
	case roleReorder:
		rep, err = runReorderer()
	default:
		err = fmt.Errorf("unknown helper role %q", role)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	out, err := json.Marshal(rep)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return 1
	}

	fmt.Println(string(out))

	return 0
}

// runWriter fills its share of a deliberately saturated id space.
func runWriter() (report, error) {
	var rep report

	worker, items, space, err := writerParams()
	if err != nil {
		return rep, err
	}

	draws := 0

	// The step is prime, so as draws increases the sequence walks EVERY residue
	// of the space before repeating one. A writer therefore finds a free slot if
	// one exists at all, and the saturated tail of the run is forced through the
	// collision path instead of around it.
	reminders, err := OpenExisting(Config{Dir: os.Getenv(envStore)}, WithIDFunc(func() string {
		draws++

		return fmt.Sprintf("c%d", (worker*7919+draws*104729)%space)
	}))
	if err != nil {
		return rep, err
	}

	defer func() { _ = reminders.Close() }()

	for seq := range items {
		if _, err := reminders.Add(raceBody(worker, seq)); err != nil {
			return rep, fmt.Errorf("worker %d seq %d: %w", worker, seq, err)
		}

		rep.Placed++
	}

	rep.Draws = draws

	return rep, nil
}

// runReader lists the store over and over while the writers work, and checks
// every body it sees against the one its tag says it must be.
func runReader() (report, error) {
	var rep report

	reminders, err := OpenExisting(Config{Dir: os.Getenv(envStore)})
	if err != nil {
		return rep, err
	}

	defer func() { _ = reminders.Close() }()

	done := os.Getenv(envDone)

	for range maxReaderRounds {
		items, err := reminders.List()
		if err != nil {
			return rep, err
		}

		for _, item := range items {
			rep.Reads++

			if _, ok := raceTag(item.Text); !ok {
				rep.Torn++
			}
		}

		rep.Rounds++

		// Termination is a marker the parent writes once every writer has
		// exited, not a deadline: a slow machine reads more, never less.
		if _, err := os.Stat(done); err == nil {
			break
		}
	}

	return rep, nil
}

// runReorderer contends for the order document from its own process.
func runReorderer() (report, error) {
	var rep report

	reminders, err := OpenExisting(Config{Dir: os.Getenv(envStore)})
	if err != nil {
		return rep, err
	}

	defer func() { _ = reminders.Close() }()

	worker, rounds, _, err := writerParams()
	if err != nil {
		return rep, err
	}

	// Counting attempts is what lets the parent bound the head from ABOVE as
	// well as below; see the assertions in TestStore_ReorderConcurrent.
	reminders.hookBeforePublish = func() { rep.Attempts++ }

	for round := range rounds {
		items, err := reminders.List()
		if err != nil {
			return rep, err
		}

		if len(items) == 0 {
			continue
		}

		// Each process bumps a different item, so the final order is whatever
		// the last writer computed — which is exactly the state the gate checks
		// for completeness rather than for a particular sequence.
		front := items[(worker+round)%len(items)].ID
		if err := reminders.Reorder([]string{front}); err != nil {
			return rep, fmt.Errorf("worker %d round %d: %w", worker, round, err)
		}

		rep.Rounds++
	}

	return rep, nil
}

func writerParams() (worker, items, space int, err error) {
	if worker, err = envInt(envWorker); err != nil {
		return 0, 0, 0, err
	}

	if items, err = envInt(envItems); err != nil {
		return 0, 0, 0, err
	}

	if space, err = envInt(envSpace); err != nil {
		return 0, 0, 0, err
	}

	return worker, items, space, nil
}

func envInt(name string) (int, error) {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}

	return value, nil
}

// raceBody is the exact body worker/seq must publish.
func raceBody(worker, seq int) string {
	return fmt.Sprintf("worker=%d seq=%d\n%s\nEND", worker, seq, strings.Repeat("X", raceFiller))
}

// raceTag recovers the worker/seq a body declares, and confirms the body is
// byte-for-byte what that pair should have written. A torn, truncated or
// interleaved write fails the comparison rather than merely looking odd.
func raceTag(text string) (string, bool) {
	line, _, ok := strings.Cut(text, "\n")
	if !ok {
		return "", false
	}

	var worker, seq int
	if _, err := fmt.Sscanf(line, "worker=%d seq=%d", &worker, &seq); err != nil {
		return "", false
	}

	if text != raceBody(worker, seq) {
		return "", false
	}

	return line, true
}

type helperProc struct {
	cmd    *exec.Cmd
	stdout bytes.Buffer
	stderr bytes.Buffer
}

// startHelper re-execs this test binary with the given role.
func startHelper(t *testing.T, env map[string]string) *helperProc {
	t.Helper()

	proc := &helperProc{cmd: exec.CommandContext(t.Context(), os.Args[0])}
	proc.cmd.Env = os.Environ()

	for name, value := range env {
		proc.cmd.Env = append(proc.cmd.Env, name+"="+value)
	}

	proc.cmd.Stdout = &proc.stdout
	proc.cmd.Stderr = &proc.stderr

	require.NoError(t, proc.cmd.Start())

	return proc
}

func (p *helperProc) wait(t *testing.T) report {
	t.Helper()

	require.NoErrorf(t, p.cmd.Wait(), "helper failed: %s", p.stderr.String())

	var rep report

	require.NoErrorf(t, json.Unmarshal(p.stdout.Bytes(), &rep), "helper output: %q", p.stdout.String())

	return rep
}

// pristineStore creates the store this run will use and nothing else.
//
// PRISTINE is load-bearing: a fixture that carried state between phases once
// reported a stale file as 733 torn reads and a duplicate, which is
// indistinguishable from the corruption the gate exists to catch.
func pristineStore(t *testing.T) string {
	t.Helper()

	parent := *raceStoreDir
	if parent == "" {
		parent = t.TempDir()
	}

	// A fresh, uniquely named subdirectory even under a caller-supplied parent:
	// the gate is only valid against a store this run created, and it must never
	// be tempted to clear a directory someone else owns.
	//nolint:usetesting // t.TempDir takes no parent, and pointing the gates at a
	// chosen filesystem is this helper's entire purpose. When no filesystem is
	// chosen, parent IS t.TempDir above.
	dir, err := os.MkdirTemp(parent, "canga-gate-")
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })

	dir = filepath.Join(dir, "store")

	reminders, err := Open(Config{Dir: dir, Repo: "example.test/acme/widget", Scope: scopeName})
	require.NoError(t, err)
	require.NoError(t, reminders.Close())

	return dir
}

// ---------------------------------------------------------------------------
// The race gate.
// ---------------------------------------------------------------------------

func TestStore_AddConcurrent(t *testing.T) {
	t.Parallel()

	writers := *raceProcs
	require.Positive(t, writers, "-race-procs must be at least 1")

	total := writers * itemsPerWriter

	// The id space holds EXACTLY as many ids as there are items, so the run ends
	// saturated and the collision path is forced rather than merely available.
	// A writer walks the whole space in the worst case, so it must stay within
	// one Add's retry budget.
	require.LessOrEqualf(t, total, maxIDAttempts,
		"-race-procs=%d saturates more ids than one Add may retry", writers)

	dir := pristineStore(t)
	done := filepath.Join(t.TempDir(), "done")

	readers := make([]*helperProc, 2)
	for i := range readers {
		readers[i] = startHelper(t, map[string]string{
			envRole:  roleReader,
			envStore: dir,
			envDone:  done,
		})
	}

	procs := make([]*helperProc, writers)
	for worker := range procs {
		procs[worker] = startHelper(t, map[string]string{
			envRole:   roleWriter,
			envStore:  dir,
			envWorker: strconv.Itoa(worker),
			envItems:  strconv.Itoa(itemsPerWriter),
			envSpace:  strconv.Itoa(total),
		})
	}

	placed, draws := 0, 0

	for _, proc := range procs {
		rep := proc.wait(t)
		placed += rep.Placed
		draws += rep.Draws
	}

	require.NoError(t, os.WriteFile(done, nil, 0o600))

	reads, torn, rounds := 0, 0, 0

	for _, proc := range readers {
		rep := proc.wait(t)
		reads += rep.Reads
		torn += rep.Torn
		rounds += rep.Rounds
	}

	assert.Equal(t, total, placed, "every requested item must have been placed")
	assert.Greater(t, draws, placed, "no id ever collided: the gate proved nothing")
	assert.Positive(t, rounds, "the readers never ran")
	// Without this the torn-read assertion is vacuous: a reader that finished
	// its rounds before the writers published anything reports zero reads AND
	// zero torn. The concurrent readers are the ONLY thing that can catch a
	// partially written item, since assertStoreIntact runs once everything has
	// settled and cannot see an interleaved read.
	assert.Positive(t, reads, "the readers never saw an item, so nothing was checked")
	assert.Zero(t, torn, "a reader saw a body that was not what its tag claims")

	assertStoreIntact(t, dir, total)

	t.Logf("writers=%d placed=%d draws=%d (collisions=%d) reads=%d rounds=%d torn=%d",
		writers, placed, draws, draws-placed, reads, rounds, torn)
}

// assertStoreIntact is the part that would catch a lost or clobbered write:
// every item is present exactly once, every body is the one its own tag says it
// should be, and no staging file survived.
func assertStoreIntact(t *testing.T, dir string, total int) {
	t.Helper()

	reminders, err := OpenExisting(Config{Dir: dir})
	require.NoError(t, err)

	defer func() { require.NoError(t, reminders.Close()) }()

	items, err := reminders.List()
	require.NoError(t, err)
	require.Len(t, items, total, "an item was lost or an extra one appeared")

	ids := make(map[string]bool, len(items))
	tags := make(map[string]bool, len(items))

	for _, item := range items {
		assert.Falsef(t, ids[item.ID], "id %s listed twice", item.ID)
		ids[item.ID] = true

		tag, ok := raceTag(item.Text)
		require.Truef(t, ok, "item %s has a malformed body", item.ID)
		assert.Falsef(t, tags[tag], "%q was published under two ids", tag)
		tags[tag] = true
	}

	temps, err := os.ReadDir(filepath.Join(dir, tmpDir))
	require.NoError(t, err)
	assert.Empty(t, temps, "a staging file was left behind")
}

// ---------------------------------------------------------------------------
// Deterministic assertions. These carry the same properties WITHOUT
// concurrency, so a regression still fails on a runner that never happens to
// make two writers collide.
// ---------------------------------------------------------------------------

const scopeName = "repo"

func newTestStore(t *testing.T, opts ...Option) *Store {
	t.Helper()

	cfg := Config{
		Dir:   filepath.Join(t.TempDir(), "store"),
		Repo:  "example.test/acme/widget",
		Scope: scopeName,
	}

	reminders, err := Open(cfg, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reminders.Close()) })

	return reminders
}

// idsInOrder hands out exactly the given ids. Drawing more than the case
// provides fails the test, because a store that redrew more often than the case
// expects is not the case the test meant to run.
func idsInOrder(t *testing.T, ids ...string) func() string {
	t.Helper()

	drawn := 0

	return func() string {
		require.Lessf(t, drawn, len(ids), "the store drew more ids than the test provided")

		id := ids[drawn]
		drawn++

		return id
	}
}

func TestStore_AddAndGet(t *testing.T) {
	t.Parallel()

	reminders := newTestStore(t)

	item, err := reminders.Add("wire the store into the purge warning")
	require.NoError(t, err)
	assert.NotEmpty(t, item.ID)

	got, err := reminders.Get(item.ID)
	require.NoError(t, err)
	assert.Equal(t, item.Text, got.Text)
	assert.Equal(t, "example.test/acme/widget", got.Repo)
	assert.Equal(t, scopeName, got.Scope)
	// Exact, not approximate: what Add returns must be what Get returns.
	assert.True(t, got.Created.Equal(item.Created), "created %s != %s", got.Created, item.Created)
}

func TestStore_AddRejectsEmptyText(t *testing.T) {
	t.Parallel()

	reminders := newTestStore(t)

	for _, text := range []string{"", "   ", "\n\n"} {
		_, err := reminders.Add(text)
		require.ErrorIsf(t, err, ErrEmptyText, "text %q", text)
	}
}

// TestStore_PlaceRefusesToOverwrite is the primitive the whole design rests on:
// link(2) onto a name that exists refuses, and leaves what is there untouched.
func TestStore_PlaceRefusesToOverwrite(t *testing.T) {
	t.Parallel()

	reminders := newTestStore(t)
	require.NoError(t, reminders.place(Item{ID: "a", Text: "first"}))

	before, err := reminders.root.ReadFile(itemFile("a"))
	require.NoError(t, err)

	err = reminders.place(Item{ID: "a", Text: "second"})
	require.ErrorIs(t, err, fs.ErrExist)

	after, err := reminders.root.ReadFile(itemFile("a"))
	require.NoError(t, err)
	assert.Equal(t, before, after, "a refused publish must leave the file byte-identical")
}

func TestStore_AddRedrawsPastAPublishedID(t *testing.T) {
	t.Parallel()

	reminders := newTestStore(t, WithIDFunc(idsInOrder(t, "taken", "taken", "free")))

	first, err := reminders.Add("first")
	require.NoError(t, err)
	require.Equal(t, "taken", first.ID)

	second, err := reminders.Add("second")
	require.NoError(t, err)
	assert.Equal(t, "free", second.ID, "a taken id must be redrawn, never overwritten")

	kept, err := reminders.Get("taken")
	require.NoError(t, err)
	assert.Equal(t, "first", kept.Text)
}

// TestStore_AddRedrawsPastAStagedTemp pins the second of the three measured
// harness bugs: without O_EXCL on the STAGING file, two writers drawing one id
// share a temp, and whichever wins the link publishes the other's body under
// its own id.
func TestStore_AddRedrawsPastAStagedTemp(t *testing.T) {
	t.Parallel()

	reminders := newTestStore(t, WithIDFunc(idsInOrder(t, "blocked", "free")))

	staged := filepath.Join(reminders.Dir(), tmpDir, "blocked")
	require.NoError(t, os.WriteFile(staged, []byte("someone else's body"), filePerm))

	item, err := reminders.Add("mine")
	require.NoError(t, err)
	assert.Equal(t, "free", item.ID)

	_, err = reminders.Get("blocked")
	assert.ErrorIs(t, err, ErrNotFound, "a stranger's staging file must not become an item")
}

func TestStore_AddRefusesACraftedID(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"../escape", "items/../../escape", ".", "..", "", "a/b"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()

			reminders := newTestStore(t, WithIDFunc(idsInOrder(t, id)))

			_, err := reminders.Add("text")
			require.ErrorIs(t, err, ErrInvalidID)
		})
	}
}

func TestStore_Remove(t *testing.T) {
	t.Parallel()

	reminders := newTestStore(t)

	item, err := reminders.Add("first")
	require.NoError(t, err)

	require.NoError(t, reminders.Remove(item.ID))

	_, err = reminders.Get(item.ID)
	require.ErrorIs(t, err, ErrNotFound)

	// Removing it again is reported, not silently swallowed: the caller named
	// an id that is not there.
	require.ErrorIs(t, reminders.Remove(item.ID), ErrNotFound)
	require.ErrorIs(t, reminders.Remove("../escape"), ErrInvalidID)
}

func TestStore_ItemPath(t *testing.T) {
	t.Parallel()

	reminders := newTestStore(t)

	item, err := reminders.Add("first")
	require.NoError(t, err)

	path, err := reminders.ItemPath(item.ID)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(reminders.Dir(), itemsDir, item.ID+itemExt), path)

	_, err = reminders.ItemPath("nosuch")
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestStore_ListIsChronological is what lets an id carry the ordering: ids lead
// with a UTC timestamp, so plain lexical order is chronological and List never
// has to read a timestamp out of every file to sort.
func TestStore_ListIsChronological(t *testing.T) {
	t.Parallel()

	reminders := newTestStore(t, WithIDFunc(idsInOrder(t,
		"20260915T120000.000002Z-cccc",
		"20260915T120000.000000Z-aaaa",
		"20260915T120000.000001Z-bbbb")))

	for _, text := range []string{"third", "first", "second"} {
		_, err := reminders.Add(text)
		require.NoError(t, err)
	}

	items, err := reminders.List()
	require.NoError(t, err)

	texts := make([]string, 0, len(items))
	for _, item := range items {
		texts = append(texts, item.Text)
	}

	assert.Equal(t, []string{"first", "second", "third"}, texts)
}

func TestOpenExisting_RefusesToCreate(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "store")

	_, err := OpenExisting(Config{Dir: dir})
	require.ErrorIs(t, err, ErrNoStore)

	_, err = os.Stat(dir)
	assert.ErrorIs(t, err, fs.ErrNotExist, "asking about a store must not create one")
}

func TestOpen_RejectsAnEmptyDir(t *testing.T) {
	t.Parallel()

	_, err := Open(Config{})
	require.Error(t, err)
}

// TestRootContainment pins the containment the store relies on instead of
// assuming it. Every refusal below is a measured one; the last is the reason
// this module's floor is Go 1.27 rather than the 1.24 that introduced os.Root.
func TestRootContainment(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "store")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, itemsDir), dirPerm))
	require.NoError(t, os.WriteFile(filepath.Join(dir, itemsDir, "real"+itemExt), []byte("body"), filePerm))
	require.NoError(t, os.Symlink("/etc", filepath.Join(dir, itemsDir, "escape")))

	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })

	tests := []struct {
		name string
		try  func() error
	}{
		{name: "create through ..", try: func() error {
			_, err := root.OpenFile("../escape.txt", os.O_CREATE|os.O_EXCL|os.O_WRONLY, filePerm)

			return err
		}},
		{name: "create through a nested ..", try: func() error {
			_, err := root.OpenFile("items/../../escape.txt", os.O_CREATE|os.O_WRONLY, filePerm)

			return err
		}},
		{name: "link out of the root", try: func() error {
			return root.Link("items/real"+itemExt, "../escape-link")
		}},
		{name: "open through an escaping symlink", try: func() error {
			_, err := root.Open("items/escape/passwd")

			return err
		}},
		{name: "open an escaping symlink with a trailing slash", try: func() error {
			_, err := root.Open("items/escape/")

			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Error(t, tt.try())
		})
	}

	outside := filepath.Dir(dir)
	for _, name := range []string{"escape.txt", "escape-link"} {
		_, err := os.Lstat(filepath.Join(outside, name))
		assert.ErrorIsf(t, err, fs.ErrNotExist, "%s escaped the root", name)
	}
}
