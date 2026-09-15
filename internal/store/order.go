// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
)

const (
	// orderKeepVersions is how much order history survives a publish. Pruning is
	// safe because it only ever removes versions BELOW the current head.
	orderKeepVersions = 5

	// maxCASAttempts bounds the optimistic retry. Each loss means another writer
	// published first, so the loop terminates as soon as contention does.
	maxCASAttempts = 64

	// maxHeadReads bounds re-reading the head when the version we just saw was
	// pruned from under us — which needs orderKeepVersions publishes to land in
	// the gap, so one retry is already generous.
	maxHeadReads = 8
)

// ErrContended reports maxCASAttempts lost races in a row on the order
// document. Nothing was overwritten; the caller may simply try again.
var ErrContended = errors.New("order document contended")

// Reorder moves ids to the front of the listing, in the order given, and leaves
// everything else behind them in its existing relative order.
//
// This is the one operation that reads, modifies and writes the whole set, and
// it still takes no lock. The order lives in its own versioned document, so a
// reorder publishes order/<N+1> with the same link(2) that publishes an item:
// if that name exists, another writer won, and this one re-reads the new head
// and recomputes instead of overwriting. A killed process leaves no lock behind
// because there is none to leave.
//
// Add and Remove deliberately never touch the document. A new item is not in it
// and lists at the end; a removed item's stale entry is skipped on read and
// collected by the next reorder. That decoupling is what keeps them lock-free.
func (s *Store) Reorder(ids []string) error {
	for _, id := range ids {
		if err := checkID(id); err != nil {
			return err
		}
	}

	for range maxCASAttempts {
		version, current, err := s.orderHead()
		if err != nil {
			return err
		}

		present, err := s.items()
		if err != nil {
			return err
		}

		for _, id := range ids {
			if _, ok := present[id]; !ok {
				return fmt.Errorf("%w: %s", ErrNotFound, id)
			}
		}

		if s.hookBeforePublish != nil {
			s.hookBeforePublish()
		}

		won, err := s.tryPublish(version+1, mergeOrder(ids, current, present))
		if err != nil {
			return err
		}

		if won {
			return nil
		}
	}

	return ErrContended
}

// tryPublish publishes one candidate order and reports whether it won the race.
//
// Linking successfully is NOT on its own proof of a win. Pruning frees the name
// of an old version, so a writer that read a stale head can link into the hole
// one left behind — measured by the concurrency gate as 40 successful links
// against a head stuck two versions behind, i.e. two reorders that reported
// success and changed nothing. A publish therefore counts only if it is still
// the highest version afterwards. If it is not, no reader ever saw it, since
// reads only ever take the maximum, and removing it is exactly the collection
// pruning would have done anyway.
func (s *Store) tryPublish(version int, ids []string) (bool, error) {
	err := s.publishOrder(version, ids)

	switch {
	case errors.Is(err, fs.ErrExist):
		return false, nil // another writer took this version first
	case err != nil:
		return false, err
	}

	head, err := s.orderHeadVersion()
	if err != nil {
		return false, err
	}

	if head > version {
		_ = s.root.Remove(orderFile(version))

		return false, nil
	}

	// Housekeeping only, and safe by construction: pruning never touches a
	// version at or above the one just published. A failure here leaves extra
	// history, which is harmless, so it must not fail a reorder that has won.
	s.pruneOrder(version)

	return true, nil
}

// orderHead returns the highest published order version and the ids it lists.
// Version 0 with no ids is the state every new store is in, and a perfectly
// good base to publish version 1 onto.
func (s *Store) orderHead() (int, []string, error) {
	for range maxHeadReads {
		head, err := s.orderHeadVersion()
		if err != nil {
			return 0, nil, err
		}

		if head == 0 {
			return 0, nil, nil
		}

		data, err := s.root.ReadFile(orderFile(head))
		if err == nil {
			return head, parseOrder(data), nil
		}

		// Pruned between the directory read and this one, which takes
		// orderKeepVersions publishes. Re-read rather than invent a head.
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		return 0, nil, fmt.Errorf("read order %d: %w", head, err)
	}

	return 0, nil, fmt.Errorf("%w: head kept moving", ErrContended)
}

// orderHeadVersion returns the highest version number present in order/.
func (s *Store) orderHeadVersion() (int, error) {
	entries, err := fs.ReadDir(s.root.FS(), orderDir)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", orderDir, err)
	}

	head := 0

	for _, entry := range entries {
		// A name that is not a version is something a person dropped in here;
		// ignore it rather than fail the whole store over it.
		if version, ok := orderVersion(entry.Name()); ok {
			head = max(head, version)
		}
	}

	return head, nil
}

// publishOrder stages the list and links it to order/<version>. An fs.ErrExist
// from the link is not a failure, it is the CAS losing: the caller retries.
func (s *Store) publishOrder(version int, ids []string) error {
	staged, err := s.stage(strings.Join(ids, "\n") + "\n")
	if err != nil {
		return err
	}

	defer func() { _ = s.root.Remove(staged) }()

	if err := s.root.Link(staged, orderFile(version)); err != nil {
		return fmt.Errorf("publish order %d: %w", version, err)
	}

	return nil
}

// pruneOrder removes versions far enough below head to be unreachable. It is
// best effort by design: see the call site.
func (s *Store) pruneOrder(head int) {
	entries, err := fs.ReadDir(s.root.FS(), orderDir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		// The same canonical check as the read side. Without it a file named
		// "0" or "-1" parses, satisfies the bound as soon as the head reaches
		// orderKeepVersions, and the store DELETES a file it promises to ignore.
		version, ok := orderVersion(entry.Name())
		if !ok {
			continue
		}

		if version <= head-orderKeepVersions {
			_ = s.root.Remove(orderFile(version))
		}
	}
}

// stage writes body to a fresh file under tmp/ and returns its path inside the
// root. The name is random rather than derived: unlike an item, the contended
// name here is the one being published, not the one being staged.
func (s *Store) stage(body string) (string, error) {
	for range maxIDAttempts {
		name := path.Join(tmpDir, "stage-"+randomName())

		file, err := s.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, filePerm)
		if errors.Is(err, fs.ErrExist) {
			continue
		}

		if err != nil {
			return "", fmt.Errorf("stage: %w", err)
		}

		if _, err := file.WriteString(body); err != nil {
			_ = file.Close()
			_ = s.root.Remove(name)

			return "", fmt.Errorf("stage: %w", err)
		}

		if err := file.Close(); err != nil {
			_ = s.root.Remove(name)

			return "", fmt.Errorf("stage: %w", err)
		}

		return name, nil
	}

	return "", fmt.Errorf("%w for a staging file", ErrExhausted)
}

// mergeOrder puts front at the head, in the order given, keeps everything else
// behind it in its existing relative order, and drops ids with no file. That
// last part is the garbage collection the design leaves to reorder, so Remove
// never has to touch this document.
func mergeOrder(front, current []string, present map[string]Item) []string {
	merged := make([]string, 0, len(present))
	placed := make(map[string]bool, len(present))

	add := func(id string) {
		if placed[id] {
			return
		}

		if _, ok := present[id]; !ok {
			return
		}

		placed[id] = true

		merged = append(merged, id)
	}

	for _, id := range front {
		add(id)
	}

	for _, id := range current {
		add(id)
	}

	// Anything neither named nor previously ordered goes last, by id, which is
	// chronological. The published document is therefore exhaustive, even
	// though List does not require it to be.
	rest := make([]string, 0, len(present))

	for id := range present {
		if !placed[id] {
			rest = append(rest, id)
		}
	}

	slices.Sort(rest)

	for _, id := range rest {
		add(id)
	}

	return merged
}

// parseOrder reads an order document. Like decodeItem it cannot fail: an entry
// that is not a valid id is dropped, never allowed to hide the rest.
func parseOrder(data []byte) []string {
	ids := make([]string, 0, strings.Count(string(data), "\n")+1)

	for line := range strings.SplitSeq(string(data), "\n") {
		id := strings.TrimSpace(line)
		if checkID(id) == nil {
			ids = append(ids, id)
		}
	}

	return ids
}

// orderFile is an order version's path inside the root.
func orderFile(version int) string {
	return path.Join(orderDir, strconv.Itoa(version))
}

// orderVersion recovers the version a filename carries, and only if the name is
// the CANONICAL rendering of it.
//
// Accepting whatever strconv.Atoi will parse is not enough, because orderFile
// renders a version back with strconv.Itoa and the two then disagree: a file
// named "007" parses as 7, but order/7 is not where anything lives. The head
// that follows cannot be read, which is indistinguishable from one pruned from
// under us, so every read retries to its bound and the store answers
// "contended" — permanently. Measured: one stray file, and `devctl reminders
// list` exits 1 until someone deletes it by hand.
func orderVersion(name string) (int, bool) {
	version, err := strconv.Atoi(name)
	if err != nil || version <= 0 || strconv.Itoa(version) != name {
		return 0, false
	}

	return version, true
}
