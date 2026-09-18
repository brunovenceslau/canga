// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

// Package store implements canga's per-repo reminder store: a directory of
// one-file-per-item records that many processes, in many sandbox VMs, plus a
// person on the host, write to at once without ever taking a lock.
//
// Two properties do all the work.
//
// Every path is resolved through an *os.Root rooted at the store directory, so
// a crafted id cannot address anything outside it BY CONSTRUCTION rather than
// by validation. Go 1.27 is a correctness floor, not a preference: before it,
// a symlink opened with a trailing slash escaped a Root.
//
// An item is published with link(2), never by writing to its final name.
// Linking onto a name that already exists fails with fs.ErrExist and leaves the
// existing file byte-identical, so one call is both the uniqueness check and
// the atomic publish. Nothing is ever overwritten, no lock is taken, and no
// killed process can leave a stale lock behind — which is the part of a mutex
// that would actually hurt here.
package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Store layout, relative to the store directory.
const (
	itemsDir = "items" // the unit of atomicity: one file per item
	tmpDir   = "tmp"   // staging, deliberately outside the items glob
	orderDir = "order" // versioned order documents; see order.go
	itemExt  = ".md"

	// A reminder is the user's own data; nothing but the user has any business
	// reading it. 0o700/0o600 also keeps gosec's file-permission checks honest.
	dirPerm  fs.FileMode = 0o700
	filePerm fs.FileMode = 0o600

	// An id collision is answered by drawing a new id. The bound exists so a
	// genuinely exhausted id space fails loudly instead of spinning; the race
	// gate deliberately saturates a tiny space to exercise the retry.
	maxIDAttempts = 512
)

var (
	// ErrNotFound reports an id with no item behind it.
	ErrNotFound = errors.New("no such reminder")

	// ErrInvalidID reports an id that is not one ordinary path segment.
	ErrInvalidID = errors.New("invalid reminder id")

	// ErrEmptyText reports an add with nothing in it.
	ErrEmptyText = errors.New("empty reminder text")

	// ErrExhausted reports maxIDAttempts collisions in a row. In production it
	// means the id space is saturated, which 32 random bits behind a per-second
	// timestamp makes effectively impossible.
	ErrExhausted = errors.New("could not draw a free name")

	// ErrNoStore reports a store directory that does not exist yet. It is its
	// own sentinel rather than a bare fs.ErrNotExist so a caller can tell "this
	// repository has no reminders yet", which is an ordinary state, from "a file
	// under the store vanished", which is not.
	ErrNoStore = errors.New("no reminder store")
)

// Config is the identity of a store. Every field is required: Dir says where
// the store lives, and Repo and Scope are stamped into each item's header so a
// file remains self-describing after it is copied out of its directory.
type Config struct {
	Dir   string // the store directory, "<base>/<host>/<owner>/<repo>/<scope>"
	Repo  string // "<host>/<owner>/<repo>", as derived by internal/repo
	Scope string
}

// Store is an open reminder store. It holds an open directory handle, so a
// caller MUST Close it. Its methods are safe to use from several goroutines,
// and — which is the harder claim — from several processes at once.
type Store struct {
	root *os.Root
	cfg  Config

	// newID is a seam, not a setting: the race gate substitutes a generator
	// with a deliberately tiny id space so collisions actually happen instead
	// of being a theoretical branch the test never reaches.
	newID func() string

	// hookBeforePublish runs between reading the current order version and
	// publishing the next one. It is the only way to land a competing writer in
	// that window deterministically, so the CAS is asserted rather than hoped
	// for. Nil everywhere except in order_test.go.
	hookBeforePublish func()
}

// Option adjusts a Store at construction.
type Option func(*Store)

// WithIDFunc replaces the id generator. Intended for tests that need
// collisions to be reachable; production code takes the default.
func WithIDFunc(f func() string) Option {
	return func(s *Store) { s.newID = f }
}

// Open returns a Store rooted at cfg.Dir, creating the store tree if it is not
// there yet. Only an add takes this path: being asked to list, remove or
// reorder must not bring a store into existence as a side effect of the
// question.
func Open(cfg Config, opts ...Option) (*Store, error) {
	return open(cfg, true, opts)
}

// OpenExisting returns a Store rooted at cfg.Dir, or ErrNoStore if nothing has
// been recorded for that repository yet.
func OpenExisting(cfg Config, opts ...Option) (*Store, error) {
	return open(cfg, false, opts)
}

func open(cfg Config, create bool, opts []Option) (*Store, error) {
	if cfg.Dir == "" {
		return nil, errors.New("store: no directory configured")
	}

	// The tree is created BEFORE the root is opened, since os.Root can only
	// contain paths beneath a directory that already exists.
	if create {
		if err := os.MkdirAll(cfg.Dir, dirPerm); err != nil {
			return nil, fmt.Errorf("create store: %w", err)
		}
	}

	root, err := os.OpenRoot(cfg.Dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w at %s", ErrNoStore, cfg.Dir)
		}

		return nil, fmt.Errorf("open store: %w", err)
	}

	// Created even when the caller did not ask for a new store: a store dir left
	// by an older layout is repaired rather than made to fail every read.
	for _, dir := range []string{itemsDir, tmpDir, orderDir} {
		if err := root.Mkdir(dir, dirPerm); err != nil && !errors.Is(err, fs.ErrExist) {
			_ = root.Close()

			return nil, fmt.Errorf("create %s: %w", dir, err)
		}
	}

	store := &Store{root: root, cfg: cfg, newID: newID}
	for _, opt := range opts {
		opt(store)
	}

	return store, nil
}

// Close releases the store's directory handle.
func (s *Store) Close() error {
	return s.root.Close()
}

// Dir returns the store directory.
func (s *Store) Dir() string {
	return s.cfg.Dir
}

// Add writes text as a new item and returns it, with the id it was published
// under. It takes no lock: a colliding id is refused by the filesystem and
// answered with a fresh one.
func (s *Store) Add(text string) (Item, error) {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return Item{}, ErrEmptyText
	}

	item := Item{
		// Truncated to the resolution the file records, so the Item returned
		// here equals the one Get returns for the same id. A value that silently
		// disagreed with its own stored form is a trap for every caller that
		// compares them.
		Created: time.Now().UTC().Truncate(time.Microsecond),
		Repo:    s.cfg.Repo,
		Scope:   s.cfg.Scope,
		Text:    text,
	}

	for range maxIDAttempts {
		item.ID = s.newID()
		if err := checkID(item.ID); err != nil {
			return Item{}, err
		}

		err := s.place(item)

		switch {
		case err == nil:
			return item, nil
		case errors.Is(err, fs.ErrExist):
			continue // the id is taken, by a staged write or a published one
		default:
			return Item{}, err
		}
	}

	return Item{}, fmt.Errorf("%w for an item", ErrExhausted)
}

// place stages an item and publishes it with link(2).
func (s *Store) place(item Item) error {
	staged := path.Join(tmpDir, item.ID)

	// O_EXCL on the STAGING file, not only on the publish. Two writers that draw
	// the same id would otherwise both write this path, and the one that wins
	// the link could publish the other's body under its own id. Measured: the
	// first race harness missed it by naming temps per process, which the real
	// design does not.
	file, err := s.root.OpenFile(staged, os.O_CREATE|os.O_EXCL|os.O_WRONLY, filePerm)
	if err != nil {
		return fmt.Errorf("stage %s: %w", item.ID, err)
	}

	if _, err := file.WriteString(item.encode()); err != nil {
		_ = file.Close()
		_ = s.root.Remove(staged)

		return fmt.Errorf("stage %s: %w", item.ID, err)
	}

	// The close is checked before the link: a write that only fails on flush
	// must not be published.
	if err := file.Close(); err != nil {
		_ = s.root.Remove(staged)

		return fmt.Errorf("stage %s: %w", item.ID, err)
	}

	if err := s.root.Link(staged, itemFile(item.ID)); err != nil {
		_ = s.root.Remove(staged)

		return fmt.Errorf("publish %s: %w", item.ID, err)
	}

	// Once the link lands the item IS published, so there is deliberately no
	// error return past this point. Reporting a failed cleanup as a failed Add
	// would make the caller retry and publish a SECOND copy of the same
	// reminder — and a leaked staging file only ever poisons the one id it is
	// named after, which is never drawn again.
	_ = s.root.Remove(staged)

	return nil
}

// Get returns one item.
func (s *Store) Get(id string) (Item, error) {
	if err := checkID(id); err != nil {
		return Item{}, err
	}

	data, err := s.root.ReadFile(itemFile(id))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Item{}, fmt.Errorf("%w: %s", ErrNotFound, id)
		}

		return Item{}, fmt.Errorf("read %s: %w", id, err)
	}

	return decodeItem(id, data), nil
}

// List returns the store's items: those the order document names, in that
// order, then everything it does not, by id — which is chronological, since an
// id leads with its UTC timestamp.
//
// The result is a snapshot and eventually consistent by design. An item added
// during the scan may or may not appear, and one removed during it is skipped
// rather than reported as an error. That is what lets Add and Remove stay
// lock-free.
func (s *Store) List() ([]Item, error) {
	items, err := s.items()
	if err != nil {
		return nil, err
	}

	_, order, err := s.orderHead()
	if err != nil {
		return nil, err
	}

	listed := make([]Item, 0, len(items))
	placed := make(map[string]bool, len(items))

	for _, id := range order {
		item, ok := items[id]
		// An ordered id whose file is gone is skipped, not an error: that is
		// precisely why Remove never has to rewrite the order document. A later
		// reorder collects the stale entry.
		if !ok || placed[id] {
			continue
		}

		placed[id] = true

		listed = append(listed, item)
	}

	rest := make([]string, 0, len(items)-len(listed))

	for id := range items {
		if !placed[id] {
			rest = append(rest, id)
		}
	}

	slices.Sort(rest)

	for _, id := range rest {
		listed = append(listed, items[id])
	}

	return listed, nil
}

// Remove deletes one item. Removing an item another process removed first is
// benign at the filesystem level; it is still reported, so a caller that named
// an id that is not there hears about it.
func (s *Store) Remove(id string) error {
	if err := checkID(id); err != nil {
		return err
	}

	if err := s.root.Remove(itemFile(id)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}

		return fmt.Errorf("remove %s: %w", id, err)
	}

	return nil
}

// ItemPath returns the absolute path of one item's file, so it can be opened in
// an editor. It creates nothing.
func (s *Store) ItemPath(id string) (string, error) {
	if err := checkID(id); err != nil {
		return "", err
	}

	if _, err := s.root.Stat(itemFile(id)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", fmt.Errorf("%w: %s", ErrNotFound, id)
		}

		return "", fmt.Errorf("stat %s: %w", id, err)
	}

	return filepath.Join(s.cfg.Dir, itemsDir, id+itemExt), nil
}

// items reads every published item, keyed by id.
func (s *Store) items() (map[string]Item, error) {
	entries, err := fs.ReadDir(s.root.FS(), itemsDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", itemsDir, err)
	}

	items := make(map[string]Item, len(entries))

	for _, entry := range entries {
		id, ok := idFromFile(entry.Name())
		if !ok || entry.IsDir() {
			continue
		}

		data, err := s.root.ReadFile(itemFile(id))
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // removed between the directory read and this one
			}

			return nil, fmt.Errorf("read %s: %w", id, err)
		}

		items[id] = decodeItem(id, data)
	}

	return items, nil
}

// itemFile is an item's path inside the root.
func itemFile(id string) string {
	return path.Join(itemsDir, id+itemExt)
}

// idFromFile recovers the id a filename carries. The FILENAME is the id — the
// "id:" header is advisory — so a file copied to a new name still works.
func idFromFile(name string) (string, bool) {
	id, ok := strings.CutSuffix(name, itemExt)
	if !ok {
		return "", false
	}

	if err := checkID(id); err != nil {
		return "", false
	}

	return id, true
}
