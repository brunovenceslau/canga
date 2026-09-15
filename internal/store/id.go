// SPDX-FileCopyrightText: 2026 Bruno Marques Venceslau de Souza <b@venceslau.dev>
// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"
)

// idTimeLayout puts a sortable UTC timestamp at the front of every id, which is
// what makes plain lexical order chronological — List leans on that instead of
// reading a timestamp out of every file.
//
// The resolution is MICROSECONDS, not seconds, because the ordering claim has
// to hold for the case that actually happens: an agent or a script recording
// several reminders in one breath. At second resolution those ids differ only
// by their random half, and "chronological" quietly became "arbitrary".
const idTimeLayout = "20060102T150405.000000Z"

// idPattern is what an id may look like. Ids are generated here, but rm, path
// and reorder take one from the command line and the filename IS the id, so
// this is a trust boundary: one ordinary path segment, no separator, no leading
// dot. os.Root would refuse an escape anyway; this refuses it with a message
// that says which argument was wrong. Compiled once, at package scope, because
// compiling a regexp allocates.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// newID returns "<UTC timestamp>-<random>".
func newID() string {
	var suffix [4]byte

	// crypto/rand, never math/rand: sandbox VMs cloned from a single snapshot
	// share a seeded PRNG stream, which is exactly the correlated-collision case
	// this store would hit. Since Go 1.24 Read is documented never to return an
	// error — it panics internally instead — so there is no error path to take.
	_, _ = rand.Read(suffix[:])

	return time.Now().UTC().Format(idTimeLayout) + "-" + hex.EncodeToString(suffix[:])
}

// randomName returns a name for a staging file. Unlike an item id it is never
// substituted by a test, because a colliding staging name buys nothing: the
// contended name is the one being published, not the one being staged.
func randomName() string {
	var suffix [8]byte

	_, _ = rand.Read(suffix[:])

	return hex.EncodeToString(suffix[:])
}

// checkID rejects an id that is not a single ordinary path segment.
func checkID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("%w: %q", ErrInvalidID, id)
	}

	return nil
}
